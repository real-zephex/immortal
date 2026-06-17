package utils

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type MemoryRecord struct {
	ID        string
	Content   string
	UpdatedAt time.Time
}

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func AddMemory(ctx context.Context, db *sql.DB, content string) (MemoryRecord, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return MemoryRecord{}, fmt.Errorf("memory content cannot be empty")
	}

	id := hashContent(content)
	_, err := db.ExecContext(ctx,
		`INSERT INTO memories (id, content, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET updated_at = CURRENT_TIMESTAMP`,
		id, content,
	)
	if err != nil {
		return MemoryRecord{}, fmt.Errorf("failed to store memory: %w", err)
	}

	return MemoryRecord{ID: id, Content: content}, nil
}

func GetMemoryByID(ctx context.Context, db *sql.DB, id string) (MemoryRecord, error) {
	var r MemoryRecord
	err := db.QueryRowContext(ctx,
		"SELECT id, content, updated_at FROM memories WHERE id = ?", id,
	).Scan(&r.ID, &r.Content, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return MemoryRecord{}, fmt.Errorf("memory %s not found", id)
	}
	if err != nil {
		return MemoryRecord{}, fmt.Errorf("failed to get memory: %w", err)
	}
	return r, nil
}

func ListMemories(ctx context.Context, db *sql.DB) ([]MemoryRecord, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, content, updated_at FROM memories ORDER BY updated_at DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}
	defer rows.Close()

	var records []MemoryRecord
	for rows.Next() {
		var r MemoryRecord
		if err := rows.Scan(&r.ID, &r.Content, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func UpdateMemory(ctx context.Context, db *sql.DB, id string, newContent string) error {
	newContent = strings.TrimSpace(newContent)
	if newContent == "" {
		return fmt.Errorf("memory content cannot be empty")
	}

	result, err := db.ExecContext(ctx,
		"UPDATE memories SET content = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		newContent, id,
	)
	if err != nil {
		return fmt.Errorf("failed to update memory: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory %s not found", id)
	}
	return nil
}

func DeleteMemory(ctx context.Context, db *sql.DB, id string) error {
	result, err := db.ExecContext(ctx,
		"DELETE FROM memories WHERE id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory %s not found", id)
	}
	return nil
}
