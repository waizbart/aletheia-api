#!/bin/bash
set -e

HOOK_DIR=$(git rev-parse --git-dir)/hooks
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)

echo "Installing pre-push hook..."

cat > "$HOOK_DIR/pre-push" << 'EOF'
#!/bin/bash
set -e

echo "Running coverage check before push..."
bash scripts/check-coverage.sh
EOF

chmod +x "$HOOK_DIR/pre-push"
echo "Done. Pre-push hook installed at $HOOK_DIR/pre-push"
