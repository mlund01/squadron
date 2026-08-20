package mission

import (
	"os"
	"path/filepath"
	"regexp"
	"time"

	"squadron/store"
)

// debugDirTimestamp matches the _YYYYMMDD_HHMMSS suffix that
// `squadron mission -d` / `squadron chat -d` append to debug directory names.
var debugDirTimestamp = regexp.MustCompile(`_(\d{8}_\d{6})$`)

const debugDirTimeLayout = "20060102_150405"

// SweepExpiredMissionRecords deletes finished mission records older than
// ttlDays from the store. ttlDays <= 0 is a no-op (retention disabled).
// In-flight missions and chat sessions are left alone.
func SweepExpiredMissionRecords(stores *store.Bundle, ttlDays int) (int, error) {
	if ttlDays <= 0 || stores == nil || stores.Missions == nil {
		return 0, nil
	}
	if f, ok := stores.Events.(interface{ Flush() }); ok {
		f.Flush()
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -ttlDays)
	return stores.Missions.PurgeExpiredMissions(cutoff)
}

// DebugSweepRoots returns the debug/ directories that a TTL sweep should
// walk: CWD/debug (where `squadron mission -d` writes today) and
// <configPath>/debug when that resolves to a distinct path.
func DebugSweepRoots(configPath string) []string {
	seen := make(map[string]struct{}, 2)
	var roots []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	add("debug")
	if configPath != "" {
		dir := configPath
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			dir = filepath.Dir(configPath)
		}
		add(filepath.Join(dir, "debug"))
	}
	return roots
}

// SweepExpiredDebugDirs deletes per-run debug directories under roots whose
// name ends in _YYYYMMDD_HHMMSS older than ttlDays. Directories that don't
// match the timestamp pattern are left alone. ttlDays <= 0 is a no-op.
func SweepExpiredDebugDirs(ttlDays int, roots []string) (removed []string, err error) {
	if ttlDays <= 0 {
		return nil, nil
	}
	cutoff := time.Now().Add(-time.Duration(ttlDays) * 24 * time.Hour)
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}

		entries, err := os.ReadDir(abs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			m := debugDirTimestamp.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			ts, err := time.ParseInLocation(debugDirTimeLayout, m[1], time.Local)
			if err != nil {
				continue
			}
			if !ts.Before(cutoff) {
				continue
			}
			path := filepath.Join(abs, e.Name())
			if err := os.RemoveAll(path); err != nil {
				continue
			}
			removed = append(removed, path)
		}
	}
	return removed, nil
}
