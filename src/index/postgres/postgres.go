// Package postgres persists index.Record summaries to Postgres so a UI can
// list, filter, and open invocations without reading full event blobs.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/nalajala4naresh/bepper/src/index"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// listSeparator joins Pattern/Tags into their TEXT columns. Chosen instead
// of native TEXT[] columns to avoid depending on driver-specific Go-slice
// array marshaling; individual patterns/tags can't contain it.
const listSeparator = "\x1f"

const schema = `
CREATE TABLE IF NOT EXISTS invocations (
	invocation_id   TEXT PRIMARY KEY,
	command         TEXT NOT NULL DEFAULT '',
	pattern         TEXT NOT NULL DEFAULT '',
	tags            TEXT NOT NULL DEFAULT '',
	user_name       TEXT NOT NULL DEFAULT '',
	host            TEXT NOT NULL DEFAULT '',
	role            TEXT NOT NULL DEFAULT '',
	repo_url        TEXT NOT NULL DEFAULT '',
	branch_name     TEXT NOT NULL DEFAULT '',
	commit_sha      TEXT NOT NULL DEFAULT '',
	parent_run_id   TEXT NOT NULL DEFAULT '',
	run_id          TEXT NOT NULL DEFAULT '',
	success         BOOLEAN NOT NULL DEFAULT FALSE,
	bazel_exit_code TEXT NOT NULL DEFAULT '',
	duration_usec   BIGINT NOT NULL DEFAULT 0,
	action_count    BIGINT NOT NULL DEFAULT 0,
	created_at      TIMESTAMPTZ NOT NULL,
	updated_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS invocations_created_at_idx ON invocations (created_at DESC);
CREATE INDEX IF NOT EXISTS invocations_repo_url_idx ON invocations (repo_url);
CREATE INDEX IF NOT EXISTS invocations_user_name_idx ON invocations (user_name);
`

// Store indexes invocation Records in Postgres.
type Store struct {
	db *sql.DB
}

// New opens a connection pool to dsn and ensures the invocations table
// exists.
func New(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Upsert inserts or updates the row for rec.InvocationID.
func (s *Store) Upsert(ctx context.Context, rec *index.Record) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO invocations (
			invocation_id, command, pattern, tags, user_name, host, role,
			repo_url, branch_name, commit_sha, parent_run_id, run_id,
			success, bazel_exit_code, duration_usec, action_count,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (invocation_id) DO UPDATE SET
			command = EXCLUDED.command,
			pattern = EXCLUDED.pattern,
			tags = EXCLUDED.tags,
			user_name = EXCLUDED.user_name,
			host = EXCLUDED.host,
			role = EXCLUDED.role,
			repo_url = EXCLUDED.repo_url,
			branch_name = EXCLUDED.branch_name,
			commit_sha = EXCLUDED.commit_sha,
			parent_run_id = EXCLUDED.parent_run_id,
			run_id = EXCLUDED.run_id,
			success = EXCLUDED.success,
			bazel_exit_code = EXCLUDED.bazel_exit_code,
			duration_usec = EXCLUDED.duration_usec,
			action_count = EXCLUDED.action_count,
			updated_at = EXCLUDED.updated_at
	`,
		rec.InvocationID, rec.Command, strings.Join(rec.Pattern, listSeparator), strings.Join(rec.Tags, listSeparator), rec.User, rec.Host, rec.Role,
		rec.RepoURL, rec.BranchName, rec.CommitSHA, rec.ParentRunID, rec.RunID,
		rec.Success, rec.BazelExitCode, rec.DurationUsec, rec.ActionCount,
		rec.CreatedAt, rec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert invocation %q: %w", rec.InvocationID, err)
	}
	return nil
}

// Get returns the record for invocationID, or nil if it isn't indexed.
func (s *Store) Get(ctx context.Context, invocationID string) (*index.Record, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE invocation_id = $1`, invocationID)
	rec, err := scanRecord(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get invocation %q: %w", invocationID, err)
	}
	return rec, nil
}

// List returns the most recent invocations matching opts, newest first.
func (s *Store) List(ctx context.Context, opts index.ListOptions) ([]*index.Record, error) {
	query := selectColumns
	var args []any
	var where []string

	if opts.Query != "" {
		args = append(args, "%"+opts.Query+"%")
		where = append(where, fmt.Sprintf(
			`(command ILIKE $%d OR pattern ILIKE $%d OR repo_url ILIKE $%d OR branch_name ILIKE $%d OR user_name ILIKE $%d OR commit_sha ILIKE $%d)`,
			len(args), len(args), len(args), len(args), len(args), len(args),
		))
	}
	switch opts.Status {
	case "success":
		where = append(where, "success")
	case "failure":
		where = append(where, "NOT success")
	}
	if opts.Before != nil {
		args = append(args, *opts.Before)
		where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
	}
	if opts.Since != nil {
		args = append(args, *opts.Since)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if opts.Until != nil {
		args = append(args, *opts.Until)
		where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list invocations: %w", err)
	}
	defer rows.Close()

	var out []*index.Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invocation: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

const selectColumns = `
	SELECT invocation_id, command, pattern, tags, user_name, host, role,
		repo_url, branch_name, commit_sha, parent_run_id, run_id,
		success, bazel_exit_code, duration_usec, action_count,
		created_at, updated_at
	FROM invocations
`

// row is satisfied by both *sql.Row and *sql.Rows.
type row interface {
	Scan(dest ...any) error
}

func scanRecord(r row) (*index.Record, error) {
	rec := &index.Record{}
	var pattern, tags string
	err := r.Scan(
		&rec.InvocationID, &rec.Command, &pattern, &tags, &rec.User, &rec.Host, &rec.Role,
		&rec.RepoURL, &rec.BranchName, &rec.CommitSHA, &rec.ParentRunID, &rec.RunID,
		&rec.Success, &rec.BazelExitCode, &rec.DurationUsec, &rec.ActionCount,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if pattern != "" {
		rec.Pattern = strings.Split(pattern, listSeparator)
	}
	if tags != "" {
		rec.Tags = strings.Split(tags, listSeparator)
	}
	return rec, nil
}
