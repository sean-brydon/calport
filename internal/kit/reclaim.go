package kit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// ReclaimReport says what a reclaim freed, or with DryRun would free.
type ReclaimReport struct {
	DryRun    bool        `json:"dry_run"`
	Reclaimed []Reclaimed `json:"reclaimed"`
	Templates []Template  `json:"templates"`
	// Waiting are worktrees whose folder is gone but whose grace period,
	// kept so a restored worktree gets its database back, has not ended.
	Waiting []Waiting `json:"waiting"`
	Bytes   int64     `json:"bytes"`
}

type Reclaimed struct {
	Key      string `json:"key"`
	Host     string `json:"host"`
	Path     string `json:"path"`
	Database string `json:"database,omitempty"`
	Port     int    `json:"port,omitempty"`
	Bytes    int64  `json:"bytes"`
}

type Template struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

type Waiting struct {
	Key          string  `json:"key"`
	Host         string  `json:"host"`
	Path         string  `json:"path"`
	ReclaimAfter float64 `json:"reclaim_after"`
}

// ReclaimOptions select what a reclaim may free.
type ReclaimOptions struct {
	DryRun bool `json:"dry_run"`
	// Paths were removed through calport, so they are freed at once.
	Paths []string `json:"paths,omitempty"`
	// All skips the grace period for every worktree whose folder is gone.
	All bool `json:"all"`
}

// Reclaim frees what worktrees whose folder is gone still hold: their
// database, port and URL record, services and logs, and snapshot templates
// from older migrations.
func (in *Installer) Reclaim(ctx context.Context, opts ReclaimOptions) (ReclaimReport, error) {
	var report ReclaimReport
	if !in.Status().Installed {
		return report, errors.New("the Cal.com kit is not installed on this box")
	}
	args := []string{"reclaim", "--json"}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	if opts.All {
		args = append(args, "--retention-days", "0")
	}
	for _, p := range opts.Paths {
		args = append(args, "--path", p)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := in.Command(ctx, filepath.Join(in.Dir(), "cal-worktree"), args...)
	if err != nil {
		return report, fmt.Errorf("reclaim: %w", err)
	}
	if err := json.Unmarshal(out, &report); err != nil {
		return report, fmt.Errorf("reclaim printed %.200q: %w", out, err)
	}
	return report, nil
}
