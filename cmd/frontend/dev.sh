#!/bin/bash
cd "$(dirname "$0")"

echo "[Frontend] Starting Vite dev server..."
echo "  API proxy: /app/fnmusic-sync/api -> http://localhost:8080"
echo "  Make sure Go backend is running on port 8080"

# 安装依赖
pnpm install --frozen-lockfile 2>/dev/null || pnpm install

# 启动 Vite dev server
pnpm dev
