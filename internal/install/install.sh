#!/bin/sh
# Run with sh. Keep execution in main so a truncated piped download cannot install.
set -eu

fail() { printf 'bonbon installer: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'HELP'
Usage: sh install.sh [--version VERSION] [--dir DIRECTORY] [--check]

Install BonBon for macOS using curl and the tools included with macOS.
Optional: GITHUB_TOKEN authenticates GitHub API requests.

  --version VERSION  Install a specific release, such as 0.0.1 or v0.0.1.
                     Default: the latest published full release.
  --dir DIRECTORY    Installation directory. Default: ~/.local/bin, or
                     BONBON_INSTALL_DIR when set. Must be an absolute path.
  --check            Compare the executable in that directory with the selected
                     release without installing or changing files.
  --help             Show this help.

Run bonbon update to upgrade. The installer never starts or stops a server, changes shell
profiles, or deletes history. Restart the server yourself when ready.
HELP
}

valid_version() {
    printf '%s\n' "$1" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
}

# Pass an optional token through stdin, never in process arguments or a file.
fetch() {
    {
        if [ -n "$github_token" ]; then
            printf 'header = "Authorization: Bearer %s"\n' "$github_token"
        fi
    } | curl -q --config - --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --fail --location --silent --show-error --connect-timeout 15 --max-time 300 \
        --retry 2 --header "Accept: $2" --output "$3" "$api/$1"
}

download_asset() {
    index=0
    while name=$(plutil -extract "assets.$index.name" raw -o - "$metadata" 2>/dev/null); do
        if [ "$name" = "$1" ]; then
            id=$(plutil -extract "assets.$index.id" raw -o - "$metadata") || fail "invalid asset ID"
            case "$id" in ''|*[!0-9]*) fail "invalid asset ID" ;; esac
            fetch "releases/assets/$id" application/octet-stream "$temporary/$1" || fail "download failed: $1"
            return
        fi
        index=$((index + 1))
    done
    fail "release $tag is missing $1"
}

main() {
    requested=latest
    install_dir=${BONBON_INSTALL_DIR:-"${HOME:?HOME must be set}/.local/bin"}
    check=false
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --version|--dir)
                [ "$#" -ge 2 ] || fail "$1 requires a value"
                case "$1" in
                    --version) requested=${2#v} ;;
                    --dir) install_dir=$2 ;;
                esac
                shift 2 ;;
            --check) check=true; shift ;;
            --help|-h) usage; return ;;
            *) fail "unknown argument: $1 (use --help)" ;;
        esac
    done
    case "$install_dir" in /*) ;; *) fail "installation directory must be absolute" ;; esac
    if [ "$requested" != latest ]; then
        valid_version "$requested" || fail "version must have the form 0.0.1"
    fi
    [ "$(uname -s)" = Darwin ] || fail "this release supports macOS only"
    case "$(uname -m)" in
        arm64) arch=arm64 ;;
        x86_64) arch=amd64 ;;
        *) fail "unsupported macOS architecture" ;;
    esac
    for tool in curl plutil tar shasum; do
        command -v "$tool" >/dev/null 2>&1 || fail "required macOS tool is missing: $tool"
    done
    github_token=${GITHUB_TOKEN:-}
    unset GITHUB_TOKEN
    case "$github_token" in *[!A-Za-z0-9_.-]*) fail "invalid characters in GITHUB_TOKEN" ;; esac
    api=https://api.github.com/repos/gyson/bonbon
    temporary=$(mktemp -d "${TMPDIR:-/tmp}/bonbon-download.XXXXXX")
    staging=
    trap 'rm -rf "$temporary"; if [ -n "$staging" ]; then rm -rf "$staging"; fi' EXIT
    trap 'exit 1' HUP INT TERM
    metadata=$temporary/release.json
    if [ "$requested" = latest ]; then
        release_path=releases/latest
    else
        release_path=releases/tags/v$requested
    fi
    fetch "$release_path" application/vnd.github+json "$metadata" ||
        fail "cannot read release; check the version and GitHub access"
    tag=$(plutil -extract tag_name raw -o - "$metadata") || fail "invalid release metadata"
    if [ "$requested" != latest ]; then
        [ "$tag" = "v$requested" ] || fail "GitHub returned a different release than requested"
    fi
    selected=${tag#v}
    valid_version "$selected" && [ "$tag" = "v$selected" ] || fail "unsupported release tag: $tag"
    destination=$install_dir/bonbon
    [ ! -L "$destination" ] || fail "refusing to replace a symlink: $destination"
    [ ! -d "$destination" ] || fail "destination is a directory: $destination"
    comparison=
    if [ -e "$destination" ] && { "$check" || [ "$requested" = latest ]; }; then
        installed_output=$("$destination" --version) || fail "cannot read installed version"
        installed=${installed_output#bonbon }
        if valid_version "$installed" && [ "$installed_output" = "bonbon $installed" ]; then
            comparison=$(awk -v current="$installed" -v available="$selected" 'BEGIN {
                split(current, c, "."); split(available, a, ".")
                for (i = 1; i <= 3; i++) {
                    if (a[i]+0 > c[i]+0) { print "newer"; exit }
                    if (a[i]+0 < c[i]+0) { print "older"; exit }
                }
                print "equal"
            }')
        elif "$check"; then
            fail "cannot compare installed version: $installed_output"
        fi
    fi
    if "$check"; then
        printf 'Available release: %s\n' "$selected"
        if [ ! -e "$destination" ]; then
            printf 'BonBon is not installed at %s\n' "$destination"
            return
        fi
        printf 'Installed version: %s\n' "$installed"
        case "$comparison" in
            newer) printf 'Update available. Run bonbon update to install it.\n' ;;
            equal) printf 'Already up to date.\n' ;;
            older) printf 'The installed version is newer than the selected release.\n' ;;
        esac
        return
    fi
    case "$comparison" in
        equal) printf 'BonBon %s is already up to date.\n' "$installed"; return ;;
        older) printf 'BonBon %s is newer than the latest release (%s); leaving it installed.\n' "$installed" "$selected"; return ;;
    esac
    asset=bonbon_${selected}_darwin_${arch}.tar.gz
    download_asset "$asset"
    download_asset checksums.txt
    expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$temporary/checksums.txt")
    printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[0-9a-f]{64}$' || fail "missing or invalid checksum for $asset"
    [ "${#expected}" -eq 64 ] || fail "duplicate checksum for $asset"
    actual=$(shasum -a 256 "$temporary/$asset")
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || fail "checksum mismatch for $asset; existing installation was not changed"
    mkdir -p "$install_dir"
    staging=$(mktemp -d "$install_dir/.bonbon-install.XXXXXX")
    # Stream only the executable, without extracting arbitrary archive paths.
    tar -xzOf "$temporary/$asset" bonbon > "$staging/bonbon" || fail "invalid release archive"
    chmod 755 "$staging/bonbon"
    actual_version=$("$staging/bonbon" --version) || fail "downloaded executable cannot run on this Mac"
    [ "$actual_version" = "bonbon $selected" ] || fail "downloaded executable reports the wrong version"
    # Rename on the same filesystem; do not truncate an executable used by a server.
    mv -f "$staging/bonbon" "$destination"
    printf 'Installed BonBon %s at %s\n' "$selected" "$destination"
    case ":$PATH:" in
        *":$install_dir:"*) ;;
        *) printf 'Add this directory to PATH in your shell configuration: %s\n' "$install_dir" ;;
    esac
    printf '\nStart with: bonbon server start\nThen open:  bonbon ui\n'
    printf 'For an existing server, run bonbon server restart when ready. Restart ends active sessions.\n'
}

main "$@"
