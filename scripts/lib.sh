#!/usr/bin/env bash
#
# scripts/format.sh 使用的 shell 工具函数。
# 单独放一个文件，把计时逻辑与格式化实现分开。

# 日志前缀，由使用方设置（如 "[Format]"），run_step 用它拼装输出。
LOG_PREFIX="${LOG_PREFIX:-}"

# now_ms：返回当前 Unix 毫秒时间戳。
# 跨平台降级链（按可用性依次尝试）：
#   1. EPOCHREALTIME —— bash 5+ 内建，精度最高，无外部依赖
#   2. date +%s%3N  —— GNU coreutils（Linux 容器里可用）
#   3. perl         —— macOS 自带（Time::HiRes 精度到微秒）
#   4. date +%s     —— 兜底，仅秒级精度
now_ms() {
  local t clean frac d

  if [ -n "${EPOCHREALTIME:-}" ]; then
    t="$EPOCHREALTIME"
    clean="${t%%[!0-9]*}"   # 小数点前的秒
    frac="${t#"$clean"}"    # 小数部分
    frac="${frac//[^0-9]/}" # 去掉可能的小数点/逗号
    frac="${frac}000"       # 补足到至少 3 位
    printf '%s%s' "$clean" "${frac:0:3}"
    return 0
  fi

  d="$(date +%s%3N 2>/dev/null || true)"
  case "$d" in
    '' | *[!0-9]*)
      ;;
    *)
      printf '%s' "$d"
      return 0
      ;;
  esac

  if command -v perl >/dev/null 2>&1; then
    d="$(perl -MTime::HiRes=time -e 'printf "%d", time * 1000' 2>/dev/null || true)"
    case "$d" in
      '' | *[!0-9]*)
        ;;
      *)
        printf '%s' "$d"
        return 0
        ;;
    esac
  fi

  printf '%s' $(($(date +%s) * 1000))
}

# fmt_duration <毫秒>：把毫秒数格式化成人类友好的耗时。
fmt_duration() {
  local ms="$1"
  if [ "$ms" -ge 1000 ]; then
    printf '%d.%02ds' $((ms / 1000)) $(((ms % 1000) / 10))
  else
    printf '%dms' "$ms"
  fi
}

# run_step <标签> <函数名> [参数...]：计时执行一个函数，并打印耗时。
# 无论函数成功还是失败都会打印耗时；失败时返回函数的退出码。
#
# 注意：函数会被放在条件上下文（if 条件）中调用，bash 此时会整体抑制函数体内的
# set -e —— 所以被调用函数必须用显式的 `|| return 1` 传播错误，不能依赖 errexit。
# 否则调用方会把「工具缺失」之类的失败静默吞掉、假装通过。
run_step() {
  local label="$1" fn="$2"
  shift 2
  local start end rc=0
  start="$(now_ms)"
  if "$fn" "$@"; then
    rc=0
  else
    rc=$?
  fi
  end="$(now_ms)"
  printf '%s⏱ %s 耗时 %s\n' "$LOG_PREFIX" "$label" "$(fmt_duration $((end - start)))"
  return "$rc"
}
