package history

import (
	"context"
	"net/url"
	"path/filepath"

	"github.com/ncruces/go-sqlite3"
)

func (s *Store) readConnection(ctx context.Context) (*sqlite3.Conn, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(s.Path)}
	uri.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(1000)"}}.Encode()
	conn, err := sqlite3.OpenContext(ctx, uri.String())
	if err != nil {
		return nil, err
	}
	conn.SetInterrupt(ctx)
	conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	return conn, nil
}
