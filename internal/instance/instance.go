// Package instance stores disposable connection information beside the archive.
package instance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"bonbon/internal/protocol"
)

func Read(directory string) (protocol.ServerInfo, error) {
	var info protocol.ServerInfo
	data, err := os.ReadFile(filepath.Join(directory, "server.json"))
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	if info.Port < 1 || info.Port > 65535 || info.Instance == "" {
		return info, errors.New("invalid server.json connection information")
	}
	return info, nil
}

// Publish replaces the descriptor atomically. Readers never see partial JSON. The
// caller must hold the directory's server lock until Remove completes.
func Publish(info protocol.ServerInfo) error {
	file, err := os.CreateTemp(info.DataDir, ".server-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(info)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(info.DataDir, "server.json"))
}

func Remove(directory string) error {
	err := os.Remove(filepath.Join(directory, "server.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RemoveOwned leaves a replacement server's descriptor intact during shutdown. If the
// directory is renamed, the root keeps inspection and removal in that directory. The
// caller still holds its server lock.
func RemoveOwned(owner protocol.ServerInfo) error {
	root, err := os.OpenRoot(owner.DataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := root.ReadFile("server.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved protocol.ServerInfo
	if err = json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if owner.Instance == "" || saved.Instance != owner.Instance {
		return nil
	}
	err = root.Remove("server.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
