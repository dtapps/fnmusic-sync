#!/bin/bash
set -e

echo "[Frontend] Building Svelte frontend..."
cd "$(dirname "$0")"

# 安装依赖
pnpm install --frozen-lockfile 2>/dev/null || pnpm install

WWW_DIR="../../internal/webui/www"

# 清理旧的构建产物（保留 index.html 和 images/ 目录）
rm -f "$WWW_DIR"/app.js "$WWW_DIR"/app.css "$WWW_DIR"/app.js.map
rm -rf "$WWW_DIR"/chunks "$WWW_DIR"/assets

# Vite 构建 → 输出到 $WWW_DIR
pnpm build

# 确保 index.html 是 Go 模板版本（Vite 不生成 index.html，我们手动维护）
# 如果 index.html 不存在，创建它
if [ ! -f "$WWW_DIR/index.html" ]; then
  cat >"$WWW_DIR/index.html" <<'EOF'
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>飞牛音乐 Scrobble 代理</title>
    <link rel="stylesheet" href="{{.BaseURL}}/app.css" />
  </head>
  <body>
    <div id="app"></div>

    <script>
      window.BaseURL = '{{.BaseURL}}';
    </script>
    <script type="module" src="{{.BaseURL}}/app.js"></script>
  </body>
</html>
EOF
  echo "[Frontend] index.html created with Go template variables."
fi

# 复制静态资源（图片）
# mkdir -p "$WWW_DIR/images/"
# cp static/images/* "$WWW_DIR/images/" 2>/dev/null || true

echo "[Frontend] Build complete! Output in $WWW_DIR/"
echo "[Frontend] Files:"
ls -la "$WWW_DIR/"
