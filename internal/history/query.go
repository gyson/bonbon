package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"
)

const (
	queryRows     = 1000
	queryBytes    = 8 << 20
	querySQLBytes = 64 << 10
)

// Separate column names and rows preserve duplicate names without losing values.
// JSON encodes SQLite BLOBs as base64 and NULL as null.
type QueryResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
}

// Query uses a dedicated read-only connection, independent of terminal capture.
// SQLite's authorizer rejects writes and connection changes during preparation.
func (s *Store) Query(ctx context.Context, query string) (result QueryResult, err error) {
	result.Rows = make([][]any, 0)
	if len(query) > querySQLBytes {
		return result, errors.New("query exceeds 64 KiB")
	}
	if strings.IndexByte(query, 0) >= 0 {
		return result, errors.New("query contains a NUL byte")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			err = fmt.Errorf("read-only query: %w", err)
		}
	}()
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(s.Path)}
	uri.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(1000)"}}.Encode()
	conn, err := sqlite3.OpenContext(ctx, uri.String())
	if err != nil {
		return result, err
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
	conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	conn.Limit(sqlite3.LIMIT_COLUMN, 128)
	conn.Limit(sqlite3.LIMIT_SQL_LENGTH, querySQLBytes)
	conn.HardHeapLimit(64 << 20)
	if err = conn.SetAuthorizer(authorizeQuery); err != nil {
		return result, err
	}
	stmt, tail, err := conn.Prepare(query)
	if err != nil {
		return result, err
	}
	if stmt == nil {
		return result, errors.New("provide one read-only SQL query")
	}
	defer stmt.Close()
	// Let SQLite parse trailing whitespace and comments. Never execute a second statement.
	extra, _, err := conn.Prepare(tail)
	if extra != nil {
		extra.Close()
	}
	if err != nil || extra != nil {
		return result, errors.New("provide exactly one read-only SQL query")
	}
	if !stmt.ReadOnly() || stmt.ColumnCount() == 0 {
		return result, errors.New("provide a SELECT, WITH ... SELECT, or VALUES query")
	}
	result.Columns = make([]string, stmt.ColumnCount())
	for i := range result.Columns {
		result.Columns[i] = stmt.ColumnName(i)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	size := len(encoded)
	for stmt.Step() {
		if len(result.Rows) == queryRows {
			result.Truncated = true
			break
		}
		values := make([]any, len(result.Columns))
		for i := range values {
			switch stmt.ColumnType(i) {
			case sqlite3.INTEGER:
				values[i] = stmt.ColumnInt64(i)
			case sqlite3.FLOAT:
				values[i] = stmt.ColumnFloat(i)
			case sqlite3.TEXT:
				values[i] = stmt.ColumnText(i)
			case sqlite3.BLOB:
				values[i] = stmt.ColumnBlob(i, []byte{})
			}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return result, err
		}
		if size+len(encoded)+1 > queryBytes {
			result.Truncated = true
			break
		}
		size += len(encoded) + 1
		result.Rows = append(result.Rows, values)
	}
	return result, stmt.Err()
}

func authorizeQuery(action sqlite3.AuthorizerActionCode, _, function, _, _ string) sqlite3.AuthorizerReturnCode {
	switch action {
	case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_RECURSIVE:
		return sqlite3.AUTH_OK
	case sqlite3.AUTH_FUNCTION:
		// BonBon installs no file or extension functions. Never allow them here.
		switch strings.ToLower(function) {
		case "load_extension", "readfile", "writefile":
			return sqlite3.AUTH_DENY
		}
		return sqlite3.AUTH_OK
	default:
		return sqlite3.AUTH_DENY
	}
}
