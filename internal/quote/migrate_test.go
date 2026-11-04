package quote

import (
	"fmt"
	"io/fs"
	"regexp"
	"testing"
)

// The runner applies the files in name order, so every name needs a unique, increasing number.
func TestMigrationFilesAreNumbered(t *testing.T) {
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations: %v", err)
	}
	name := regexp.MustCompile(`^migrations/(\d{4})_[a-z0-9_]+\.sql$`)
	for i, file := range files {
		m := name.FindStringSubmatch(file)
		if m == nil {
			t.Fatalf("%s: want migrations/NNNN_name.sql", file)
		}
		if want := fmt.Sprintf("%04d", i+1); m[1] != want {
			t.Errorf("%s: migrations are numbered 0001, 0002... without gaps, want %s", file, want)
		}
	}
}
