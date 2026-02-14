#!/bin/bash
# prep-review-test.sh - prepare toy project for review-only e2e testing
# creates code with subtle issues that trigger multiple review iterations

set -e

TEST_DIR="/tmp/ralphex-review-test"

echo "cleaning up previous test..."
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR/.bin"
cd "$TEST_DIR"

echo "initializing git repo..."
git init -q
git config user.email "test@test.com"
git config user.name "Test"

echo "creating go module..."
cat > go.mod << 'EOF'
module reviewtest

go 1.23
EOF

echo "creating main.go with subtle issues..."
cat > main.go << 'EOF'
package main

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// Config holds application configuration
type Config struct {
	DataDir  string
	MaxItems int
	Debug    bool
}

func main() {
	cfg := loadConfig()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() *Config {
	return &Config{
		DataDir:  os.Getenv("DATA_DIR"),
		MaxItems: 100,
		Debug:    os.Getenv("DEBUG") == "1",
	}
}

func run(cfg *Config) error {
	items, err := fetchItems(cfg.DataDir)
	if err != nil {
		return err
	}

	// subtle: no limit check against cfg.MaxItems
	results := processItems(items)

	for _, r := range results {
		fmt.Println(r)
	}
	return nil
}

func fetchItems(dir string) ([]string, error) {
	// subtle: dir could be empty, no validation
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var items []string
	for _, e := range entries {
		if !e.IsDir() {
			items = append(items, e.Name())
		}
	}
	return items, nil
}

func processItems(items []string) []string {
	var wg sync.WaitGroup
	results := make([]string, len(items))

	// subtle: race condition - writing to shared slice without proper indexing
	for i, item := range items {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			results[idx] = transform(name)
		}(i, item)
	}

	wg.Wait()
	return results
}

func transform(s string) string {
	// subtle: no nil/empty check, panics on edge cases not obvious
	if len(s) < 3 {
		return s
	}
	return s[:3] + "..."
}
EOF

echo "creating .gitignore..."
cat > .gitignore << 'EOF'
.ralphex/progress/
.bin/
EOF

echo "creating initial commit on master (minimal)..."
git add go.mod .gitignore
git commit -q -m "initial setup"

echo "creating feature branch..."
git checkout -q -b feature-data-processor

echo "committing code on feature branch..."
git add main.go
git commit -q -m "feat: add data processor"

echo ""
echo "review test project ready at: $TEST_DIR"
echo "run: cd $TEST_DIR && ralphex --review"
echo ""
echo "subtle issues for reviewers to find:"
echo "  - empty DataDir not validated before use"
echo "  - MaxItems config not enforced"
echo "  - potential race in processItems (though currently safe)"
echo "  - transform() edge case with short strings"
