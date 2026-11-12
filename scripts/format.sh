#!/usr/bin/env bash
#
# 配置与文档类格式化入口：YAML / JSON / SQL / Shell / Markdown / Dockerfile。
#
# 不含 Go / 前端：gofmt、pnpm format 属于必需工具链，缺失应直接失败，
# 由 Makefile 的 format-backend / format-frontend 负责；全量格式化用 `make format`。
#
# 为什么要独立成脚本，而不是写在 Makefile 里：
#   Make 的每个 recipe 行都是独立的 shell，`|| { ...; exit 0; }` 里的 exit 0
#   只结束当前那一行的 shell，make 仍会继续执行后续 recipe 行。于是「工具未安装」
#   的提示打完之后照样往下调用工具，报 "not found" 并让 make 以非 0 退出。
#   放进单个 shell 脚本后，if/else 的分支天然正确：缺失即跳过，不再误报失败。
#
# 用法：
#   scripts/format.sh              # 上述 6 类（不含 Go / 前端）
#   scripts/format.sh yaml         # 仅 YAML
#   scripts/format.sh json         # 仅 JSON
#   scripts/format.sh sql          # 仅 SQL
#   scripts/format.sh shell        # 仅 Shell 脚本
#   scripts/format.sh markdown     # 仅 Markdown
#   scripts/format.sh docker       # 仅 Dockerfile
#
# 移植到其它项目：
#   只改文件顶部的「可自定义配置区」（工具、参数、扫描路径），函数体无需改动。
#
# 环境变量：
#   FORMAT_STRICT=1   工具缺失时以非 0 退出（CI 想强制要求时用）

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# shellcheck source=scripts/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

###############################################################################
# 可自定义配置区（复制到其它项目时，只改这一段即可）
#
# - *_CMD / *_INSTALL：工具命令名、缺失时的安装提示（提示只打印，不会执行）
# - *_ARGS：传给工具的参数；数组可以留空，写成 ()
# - *_TARGETS / *_DIRS：路径相对仓库根目录，不存在或配置为空则跳过该步骤
# - *_EXCLUDES：find -path 的 glob 模式，不是具体路径，不做存在性检查
# - *_GLOB：文件名匹配（供 find -name 使用）
#
# 数组统一用 ${arr[@]+"${arr[@]}"} 展开：macOS 自带 bash 3.2 在 set -u 下
# 展开空数组会报 unbound variable，这样写才能兼容且允许把数组留空。
###############################################################################

LOG_PREFIX="[Format] "

# 工具命令 / 安装提示
YAMLFMT_CMD="yamlfmt"
YAMLFMT_INSTALL="go install github.com/google/yamlfmt/cmd/yamlfmt@latest"
JSONFMT_CMD="gojq"
JSONFMT_INSTALL="go install github.com/itchyny/gojq/cmd/gojq@latest"
SQLFMT_CMD="sql-formatter"
SQLFMT_INSTALL="go install github.com/wasilibs/go-sql-formatter/v15/cmd/sql-formatter@latest"
MARKDOWNFMT_CMD="markdownfmt"
MARKDOWNFMT_INSTALL="go install github.com/Kunde21/markdownfmt@latest"
SHFMT_CMD="shfmt"
SHFMT_INSTALL="go install mvdan.cc/sh/v3/cmd/shfmt@latest"
DOCKERFMT_CMD="dockerfmt"
DOCKERFMT_INSTALL="go install github.com/reteps/dockerfmt@latest"

# YAML：配置文件（留空或文件不存在则用工具默认配置）、额外参数、目标文件/目录
YAMLFMT_CONF=".yamlfmt"
YAMLFMT_ARGS=()
YAML_TARGETS=(
  ".cnb"
  ".gitcode"
  ".github"
  ".gitlab"
  ".workflow"
  "cmd"
  ".cnb.yml"
  ".gitlab-ci.yml"
  ".golangci.yml"
  ".nfpm.yaml"
)
YAML_EXCLUDES=(
  "*/node_modules/*"
  "cmd/frontend/pnpm-lock.yaml"
)

# JSON：工具参数（gojq 过滤器）、扫描目录、文件名匹配、排除路径
# 当前项目未启用；JSON_TARGETS=() 即整个步骤跳过（不检查工具、不扫描文件），
# 复制到别的项目时按实际目录填写，不存在的目录会自动跳过。
JSONFMT_ARGS=(.)
JSON_TARGETS=(
  "cmd/frontend/"
  "fpkg/wizard/"
  "fpkg/app/ui/"
  "fpkg/config/"
)
JSON_GLOB="*.json"
JSON_EXCLUDES=(
  '*/node_modules/*'
)

# SQL：工具参数（-l 指定方言）、扫描目录、文件名匹配、排除路径
# 当前项目 SQL 快照在 sql/；复制到别的项目时按实际目录改 SQL_TARGETS，
# 不存在的目录会自动跳过，SQL_TARGETS=() 即整个步骤跳过。
SQLFMT_ARGS=(--fix -l sqlite)
SQL_TARGETS=(
  "internal/db"
  "internal/datadb"
)
SQL_GLOB="*.sql"
SQL_EXCLUDES=()

# Markdown：工具参数、文件名匹配、排除路径
# MARKDOWN_EXCLUDES 是 find -path 的 glob 模式，不是待检查的路径，无需存在。
MARKDOWNFMT_ARGS=(-w)
MARKDOWN_GLOB="*.md"
MARKDOWN_EXCLUDES=(
  '*/node_modules/*'
  './.git/*'
  './.codebuddy/*'
  './.trae/*'
)

# Shell：shfmt 参数、目标目录（SHELL_TARGETS=() 即跳过该步骤）
SHFMT_ARGS=(-w -i 2 -ci -bn -s -ln bash)
SHELL_TARGETS=(
  "cmd/frontend/"
  "scripts/"
  "fpkg/cmd/*"
)

# Dockerfile：扫描目录、文件名匹配、dockerfmt 参数
DOCKER_DIRS=(
  ".cnb"
  ".gitcode"
  ".github"
  ".gitlab"
  ".workflow"
)
DOCKER_GLOB="Dockerfile*"
DOCKERFMT_ARGS=(-w)

###############################################################################

STRICT="${FORMAT_STRICT:-0}"

log() {
  printf '%s%s\n' "$LOG_PREFIX" "$*"
}

# skip <工具名> <安装方法> <描述>
# 工具缺失时的处理：默认打印提示后跳过（退出码 0）；FORMAT_STRICT=1 时直接失败。
#
# 注意：本脚本不会自动安装任何工具，第二个参数只是「安装方法」的文字说明，
# 仅供打印，不会被执行。
skip() {
  local tool="$1" install="$2" desc="$3"
  if [ "$STRICT" = "1" ]; then
    printf '%s❌ %s 未安装（FORMAT_STRICT=1，无法跳过）\n' "$LOG_PREFIX" "$tool" >&2
    printf '   本脚本不会自动安装工具，请自行安装后再重试。\n' >&2
    printf '   安装方式: %s\n' "$install" >&2
    return 1
  fi
  printf '%s⚠️  %s 未安装，跳过 %s 格式化。\n' "$LOG_PREFIX" "$tool" "$desc"
  printf '   本脚本不会自动安装工具，如需格式化请自行安装。\n'
  printf '   安装方式: %s\n' "$install"
  return 0
}

# targets_exist <标签> <路径...>：过滤掉不存在的目标，存在的写入 FILTERED。
# 配置为空或全部目标不存在时返回 1，调用方据此跳过，不调用工具。
FILTERED=()
targets_exist() {
  local label="$1" p count=0
  shift
  FILTERED=()
  for p in "$@"; do
    if [ -e "$p" ]; then
      FILTERED+=("$p")
      count=$((count + 1))
    else
      log "跳过不存在的 $label 目标: $p"
    fi
  done
  if [ "$count" -eq 0 ]; then
    log "没有可格式化的 $label 目标，跳过。"
    return 1
  fi
  return 0
}

fmt_yaml() {
  targets_exist "YAML" ${YAML_TARGETS[@]+"${YAML_TARGETS[@]}"} || return 0
  if ! command -v "$YAMLFMT_CMD" >/dev/null 2>&1; then
    skip "$YAMLFMT_CMD" "$YAMLFMT_INSTALL" "YAML" || return 1
    return 0
  fi

  local -a yaml_args=()
  if [ -n "$YAMLFMT_CONF" ]; then
    if [ -f "$YAMLFMT_CONF" ]; then
      yaml_args=(-conf "$YAMLFMT_CONF")
    else
      log "未找到 ${YAMLFMT_CONF}，按 ${YAMLFMT_CMD} 默认配置格式化。"
    fi
  fi

  local dir f p
  local -a find_args=()
  for p in ${YAML_EXCLUDES[@]+"${YAML_EXCLUDES[@]}"}; do
    find_args+=(-not -path "$p")
  done

  # 自己收集 YAML 文件再交给 yamlfmt：yamlfmt 自带的 -exclude 只按目录名匹配，
  # 与其它步骤统一用 find -path 排除（YAML_EXCLUDES）的口径对不上。
  # 目录递归收集，单文件目标直接取用。
  local -a yaml_files=()
  for dir in ${FILTERED[@]+"${FILTERED[@]}"}; do
    if [ -d "$dir" ]; then
      while IFS= read -r -d '' f; do
        yaml_files+=("$f")
      done < <(find "$dir" -type f \( -name '*.yml' -o -name '*.yaml' \) ${find_args[@]+"${find_args[@]}"} -print0)
    elif [ -f "$dir" ]; then
      yaml_files+=("$dir")
    fi
  done

  if [ "${#yaml_files[@]}" -eq 0 ]; then
    log "没有可格式化的 YAML 文件，跳过。"
    return 0
  fi

  log "使用 $YAMLFMT_CMD 格式化 YAML（${#yaml_files[@]} 个文件）..."
  "$YAMLFMT_CMD" ${yaml_args[@]+"${yaml_args[@]}"} ${YAMLFMT_ARGS[@]+"${YAMLFMT_ARGS[@]}"} "${yaml_files[@]}" || {
    printf '❌ %s 格式化失败\n' "$YAMLFMT_CMD" >&2
    return 1
  }
  log "YAML 格式化完成（${YAMLFMT_CMD}）。"
}

fmt_markdown() {
  if ! command -v "$MARKDOWNFMT_CMD" >/dev/null 2>&1; then
    skip "$MARKDOWNFMT_CMD" "$MARKDOWNFMT_INSTALL" "Markdown" || return 1
    return 0
  fi

  local f p
  local -a find_args=()
  for p in ${MARKDOWN_EXCLUDES[@]+"${MARKDOWN_EXCLUDES[@]}"}; do
    find_args+=(-not -path "$p")
  done

  log "使用 $MARKDOWNFMT_CMD 格式化 Markdown..."
  while IFS= read -r -d '' f; do
    "$MARKDOWNFMT_CMD" ${MARKDOWNFMT_ARGS[@]+"${MARKDOWNFMT_ARGS[@]}"} "$f" || {
      printf '❌ %s 格式化失败: %s\n' "$MARKDOWNFMT_CMD" "$f" >&2
      return 1
    }
  done < <(find . -type f -name "$MARKDOWN_GLOB" ${find_args[@]+"${find_args[@]}"} -print0)
  log "Markdown 格式化完成（${MARKDOWNFMT_CMD}）。"
}

fmt_shell() {
  targets_exist "Shell" ${SHELL_TARGETS[@]+"${SHELL_TARGETS[@]}"} || return 0
  if ! command -v "$SHFMT_CMD" >/dev/null 2>&1; then
    skip "$SHFMT_CMD" "$SHFMT_INSTALL" "Shell" || return 1
    return 0
  fi

  log "使用 $SHFMT_CMD 格式化 Shell..."
  "$SHFMT_CMD" ${SHFMT_ARGS[@]+"${SHFMT_ARGS[@]}"} ${FILTERED[@]+"${FILTERED[@]}"} || {
    printf '❌ %s 格式化失败\n' "$SHFMT_CMD" >&2
    return 1
  }
  log "Shell 格式化完成（${SHFMT_CMD}）。"
}

fmt_docker() {
  targets_exist "Dockerfile" ${DOCKER_DIRS[@]+"${DOCKER_DIRS[@]}"} || return 0
  if ! command -v "$DOCKERFMT_CMD" >/dev/null 2>&1; then
    skip "$DOCKERFMT_CMD" "$DOCKERFMT_INSTALL" "Dockerfile" || return 1
    return 0
  fi

  log "使用 $DOCKERFMT_CMD 格式化 Dockerfile..."
  local dir
  for dir in ${FILTERED[@]+"${FILTERED[@]}"}; do
    [ -d "$dir" ] || {
      log "跳过非目录的 Dockerfile 目标: $dir"
      continue
    }
    find "$dir" -type f -name "$DOCKER_GLOB" \
      -exec "$DOCKERFMT_CMD" ${DOCKERFMT_ARGS[@]+"${DOCKERFMT_ARGS[@]}"} {} + || {
      printf '❌ %s 格式化失败\n' "$DOCKERFMT_CMD" >&2
      return 1
    }
  done
  log "Dockerfile 格式化完成（${DOCKERFMT_CMD}）。"
}

fmt_json() {
  targets_exist "JSON" ${JSON_TARGETS[@]+"${JSON_TARGETS[@]}"} || return 0
  if ! command -v "$JSONFMT_CMD" >/dev/null 2>&1; then
    skip "$JSONFMT_CMD" "$JSONFMT_INSTALL" "JSON" || return 1
    return 0
  fi

  local dir f p tmp count=0
  local -a find_args=()
  for p in ${JSON_EXCLUDES[@]+"${JSON_EXCLUDES[@]}"}; do
    find_args+=(-not -path "$p")
  done

  log "使用 $JSONFMT_CMD 格式化 JSON..."
  for dir in ${FILTERED[@]+"${FILTERED[@]}"}; do
    [ -d "$dir" ] || {
      log "跳过非目录的 JSON 目标: $dir"
      continue
    }
    while IFS= read -r -d '' f; do
      tmp="$(mktemp "${f}.XXXXXX")" || return 1
      if ! "$JSONFMT_CMD" ${JSONFMT_ARGS[@]+"${JSONFMT_ARGS[@]}"} "$f" >"$tmp"; then
        printf '❌ %s 格式化失败: %s\n' "$JSONFMT_CMD" "$f" >&2
        rm -f "$tmp"
        return 1
      fi
      # mktemp 生成的文件是 600，写回而非 mv，避免改变原文件权限。
      if ! cat "$tmp" >"$f"; then
        printf '❌ 写回失败: %s（临时文件保留在 %s）\n' "$f" "$tmp" >&2
        return 1
      fi
      rm -f "$tmp"
      count=$((count + 1))
    done < <(find "$dir" -type f -name "$JSON_GLOB" ${find_args[@]+"${find_args[@]}"} -print0)
  done
  if [ "$count" -eq 0 ]; then
    log "没有可格式化的 JSON 文件，跳过。"
  fi
  log "JSON 格式化完成（${JSONFMT_CMD}，${count} 个文件）。"
}

fmt_sql() {
  targets_exist "SQL" ${SQL_TARGETS[@]+"${SQL_TARGETS[@]}"} || return 0
  if ! command -v "${SQLFMT_CMD}" >/dev/null 2>&1; then
    skip "${SQLFMT_CMD}" "${SQLFMT_INSTALL}" "SQL" || return 1
    return 0
  fi

  local dir f p count=0
  local -a find_args=()
  for p in ${SQL_EXCLUDES[@]+"${SQL_EXCLUDES[@]}"}; do
    find_args+=(-not -path "$p")
  done

  log "使用 ${SQLFMT_CMD} 格式化 SQL..."
  for dir in ${FILTERED[@]+"${FILTERED[@]}"}; do
    [ -d "$dir" ] || {
      log "跳过非目录的 SQL 目标: ${dir}"
      continue
    }
    while IFS= read -r -d '' f; do
      "${SQLFMT_CMD}" ${SQLFMT_ARGS[@]+"${SQLFMT_ARGS[@]}"} "$f" || {
        printf '❌ %s 格式化失败: %s\n' "${SQLFMT_CMD}" "$f" >&2
        return 1
      }
      count=$((count + 1))
    done < <(find "$dir" -type f -name "${SQL_GLOB}" ${find_args[@]+"${find_args[@]}"} -print0)
  done
  if [ "$count" -eq 0 ]; then
    log "没有可格式化的 SQL 文件，跳过。"
  fi
  log "SQL 格式化完成（${SQLFMT_CMD}，${count} 个文件）。"
}

main() {
  case "${1:-all}" in
    yaml)
      run_step "YAML 格式化" fmt_yaml
      ;;
    json)
      run_step "JSON 格式化" fmt_json
      ;;
    sql)
      run_step "SQL 格式化" fmt_sql
      ;;
    shell)
      run_step "Shell 格式化" fmt_shell
      ;;
    markdown)
      run_step "Markdown 格式化" fmt_markdown
      ;;
    docker)
      run_step "Dockerfile 格式化" fmt_docker
      ;;
    all)
      run_step "YAML 格式化" fmt_yaml
      run_step "JSON 格式化" fmt_json
      run_step "SQL 格式化" fmt_sql
      run_step "Shell 格式化" fmt_shell
      run_step "Markdown 格式化" fmt_markdown
      run_step "Dockerfile 格式化" fmt_docker
      ;;
    -h | --help)
      printf '用法: %s [yaml|json|sql|shell|markdown|docker|all]\n' "$0"
      ;;
    *)
      printf '用法: %s [yaml|json|sql|shell|markdown|docker|all]\n' "$0" >&2
      exit 2
      ;;
  esac
}

main "$@"
