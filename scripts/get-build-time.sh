#!/bin/bash
# 获取构建时间（UTC ISO 8601格式）
# 支持 Mac/Linux/Git Bash/PowerShell

# 尝试 date 命令（Mac/Linux/Git Bash）
if command -v date >/dev/null 2>&1; then
  TIMESTAMP=$(date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)
  if [ -n "$TIMESTAMP" ]; then
    echo "$TIMESTAMP"
    exit 0
  fi
fi

# 尝试 PowerShell（Windows）
if command -v powershell.exe >/dev/null 2>&1; then
  TIMESTAMP=$(powershell.exe -NoProfile -Command "Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ'" 2>/dev/null)
  if [ -n "$TIMESTAMP" ]; then
    echo "$TIMESTAMP"
    exit 0
  fi
fi

# 兜底：返回空字符串
echo ""
