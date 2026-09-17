#!/bin/bash
set -e

VERSION=$(git rev-list --count HEAD)

go build -ldflags "-X main.version=$VERSION" -o go-server .

cat > package.json <<EOF
{
  "name": "go-server",
  "version": "$VERSION"
}
EOF

echo "Built go-server $VERSION"
