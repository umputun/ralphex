#!/bin/bash
# prep-toy-test.sh - prepare toy project for e2e testing

set -e

TEST_DIR="/tmp/ralphex-test"

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
module toyproject

go 1.23
EOF

echo "creating buggy main.go..."
cat > main.go << 'EOF'
package main

import (
	"fmt"
	"os"
)

func main() {
	// bug: error not handled
	data, _ := os.ReadFile("config.txt")
	fmt.Println(string(data))
}

// unused function - should be removed
func unused() {
	fmt.Println("never called")
}
EOF

echo "creating .gitignore..."
cat > .gitignore << 'EOF'
.ralphex/progress/
.bin/
EOF

echo "creating plan file..."
mkdir -p docs/plans
cat > docs/plans/fix-issues.md << 'EOF'
# Plan: Fix Code Issues

## Overview
Fix linting issues in the toy project.

## Validation Commands
- `go build ./...`
- `go vet ./...`

### Task 1: Fix error handling
- [ ] Handle the error from os.ReadFile
- [ ] Either log and exit or handle gracefully

### Task 2: Remove unused function
- [ ] Remove the unused() function
- [ ] Verify go vet passes
EOF

echo "creating initial commit..."
git add -A
git commit -q -m "initial commit with buggy code"

echo ""
echo "toy project ready at: $TEST_DIR"
echo "run: cd $TEST_DIR && ralphex docs/plans/fix-issues.md"
