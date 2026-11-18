#!/bin/sh
# shellcheck disable=SC3043  # local 不是 POSIX 标准，但 dash / ash / bash 都支持
#
# 生成 CNB 流水线 IM 通知正文（企业微信 / 飞书 / 钉钉共用）。
#
# 三个 workflow 各自生成通知正文的逻辑原先内嵌在 YAML heredoc 里难以维护，
# 这里统一收口为 POSIX sh 实现（替代早先的 render-ci-notice.py）：
#   - sechook : 读取 cnbcool/cnb-sechook 写出的 sec_overview.md（完整扫描报告）
#   - review  : 读取 cnbcool/code-review 写出的 code_review.json（status + issues）
#   - sca     : 通过 CNB OpenAPI 读取 Commit Annotation（sca.<capability>.<mode>.<field>）
#
# 每个子命令生成两份正文：
#   - <name>_notice.md : markdown 版，「查看详情」为 [查看详情](URL) 超链接
#                        → 企业微信 fromFile、钉钉 content
#   - <name>_notice.txt: 纯文本版，URL 裸写（飞书 text 消息会自动识别为链接）
#                        → 飞书 content
# 两份都通过 CNB set-output（base64）导出，避免多行被逐行解析。
#
# 依赖镜像：docker.cnb.cool/dtapp/alpine:latest（已预装 jq / curl / coreutils / tzdata）。
# 时间：CNB_BUILD_START_TIME（UTC）经 epoch + 8h 转北京时间（UTC+8）。
#
# 用法：
#   sh scripts/ci/render-ci-notice.sh sechook [输出.md]
#   sh scripts/ci/render-ci-notice.sh review  [输出.md]
#   sh scripts/ci/render-ci-notice.sh sca full|diff [输出.md]
#   sh scripts/ci/render-ci-notice.sh --self-test
#
# ⚠️ 保持 POSIX sh 语法：CNB 的 stage 默认用 sh（dash/ash）执行。
#    不要引入 [[ ]]、数组、BASH_SOURCE、$'...' 这类 bash 专有写法。

set -eu

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

# 链接样式，由 render_* 在生成两份正文时切换：md（markdown 链接）/ text（裸 URL）
LINK_STYLE='md'

# 时间行：CNB_BUILD_START_TIME 为 UTC（如 2026-11-18T05:45:30.123Z），转北京时间。
# 先把 UTC 字符串归一化成 "YYYY-MM-DD HH:MM:SS"（去掉 T / 毫秒 / Z），
# 再统一转成 epoch 秒 + 8h，最后格式化——避免 GNU date -d 与 BSD date -j 的时区语义差异。
build_time_text() {
  local raw="${CNB_BUILD_START_TIME:-}" norm epoch result
  if [ -z "${raw}" ]; then
    printf '未知'
    return 0
  fi

  norm="$(printf '%s' "${raw}" \
    | sed -E 's/T/ /; s/\.[0-9]+Z?$//; s/Z$//')"

  epoch="$(date -u -d "${norm} UTC" '+%s' 2>/dev/null || true)"
  if [ -z "${epoch}" ]; then
    epoch="$(date -u -j -f '%Y-%m-%d %H:%M:%S' "${norm}" '+%s' 2>/dev/null || true)"
  fi
  if [ -z "${epoch}" ]; then
    printf '%s' "${raw}"
    return 0
  fi

  epoch=$((epoch + 8 * 3600))
  result="$(date -u -d "@${epoch}" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || true)"
  if [ -z "${result}" ]; then
    result="$(date -u -r "${epoch}" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || true)"
  fi
  printf '%s' "${result:-${raw}}"
}

# 所有通知共用的头信息：标题 + 时间 + 仓库 + PR 上下文 + 可点击的查看详情链接。
# header <标题> > 输出文件
header() {
  local title="$1" url="${CNB_EVENT_URL:-}"
  printf '## %s\n\n' "${title}"
  printf -- '- 时间: %s (UTC+8)\n' "$(build_time_text)"
  printf -- '- 仓库: %s\n' "${CNB_REPO_SLUG:-}"
  if [ -n "${CNB_PULL_REQUEST_TITLE:-}" ]; then
    printf -- '- PR: %s\n' "${CNB_PULL_REQUEST_TITLE}"
  fi
  if [ -n "${CNB_PULL_REQUEST_PROPOSER:-}" ]; then
    printf -- '- 发起人: %s\n' "${CNB_PULL_REQUEST_PROPOSER}"
  fi
  if [ "${LINK_STYLE}" = 'md' ]; then
    # 企业微信 / 钉钉 markdown：点击「查看详情」四个字即可跳转
    printf '[查看详情](%s)\n\n' "${url}"
  else
    # 飞书 text 消息不渲染 markdown，裸 URL 由客户端自动识别为可点击链接
    printf '查看详情: %s\n\n' "${url}"
  fi
}

# 写正文文件 + 打印 CNB set-output（base64 避免多行被逐行解析）。
# emit <输出文件> <result key>
emit() {
  local out_file="$1" key="$2" encoded
  # coreutils 的 base64 -w0 不折行；busybox 不支持 -w，兜底 tr 去换行
  encoded="$(base64 -w0 <"${out_file}" 2>/dev/null || base64 <"${out_file}" | tr -d '\n')"
  printf '##[set-output %s=base64,%s]\n' "${key}" "${encoded}"
}

# 由 .md 路径推导 .txt 路径
text_path() {
  printf '%s' "${1%.md}.txt"
}

# 由 .md 路径推导飞书 post JSON 路径
feishu_post_path() {
  printf '%s' "${1%.md}_feishu.json"
}

# 把正文里的裸 URL 转成 markdown 链接（企业微信 / 钉钉）。
# 链接文字用短词，不显示完整 URL：
#   "查看详情: URL" -> "[查看详情](URL)"
#   "- 报告: URL"   -> "- [查看报告](URL)"（去掉「报告:」前缀避免重复）
#   其他裸 URL      -> "[查看链接](URL)"
# 已经在 `](...)` 里的 URL 跳过（header 的 [查看详情](URL) 不会被二次包装）。
md_linkify() {
  awk '
    {
      line = $0; out = ""
      while (match(line, /https?:\/\/[^ )<>]+/)) {
        pre = substr(line, 1, RSTART - 1)
        url = substr(line, RSTART, RLENGTH)
        line = substr(line, RSTART + RLENGTH)
        if (pre ~ /\($/) {
          out = out pre url
        } else {
          if (pre ~ /查看详情: *$/) { sub(/查看详情: *$/, "", pre); label = "查看详情" }
          else if (pre ~ /报告: *$/) { sub(/报告: *$/, "", pre); label = "查看报告" }
          else { label = "查看链接" }
          out = out pre "[" label "](" url ")"
        }
      }
      print out line
    }
  '
}

# 把纯文本正文转成飞书 post 富文本 JSON（filepath 格式，外层包 content）。
# 每行文字作为一个 text 元素行；行内任何 URL 都拆成可点击的 a 标签。
# 「查看详情: <URL>」那行只保留一个「查看详情」a 标签。
# build_feishu_post <标题> <纯文本文件> <输出 json>
build_feishu_post() {
  local title="$1" txt_file="$2" out_file="$3"
  jq -n \
    --arg title "${title}" \
    --rawfile body "${txt_file}" \
    '
      def clean: sub("^## "; "") | sub("^### "; "") | sub("^- "; "");
      # 把一行拆成 text/a 元素数组；链接文字用短词，不显示完整 URL。
      # "查看详情: URL" -> 单个「查看详情」a；
      # "报告: URL"      -> 去掉「报告:」前缀后接「查看报告」a；
      # 其他 URL          -> 前缀文字 + 「查看链接」a。
      def row:
        clean as $line
        | if $line == "" then []
          elif ($line | startswith("查看详情: ")) then
            [{ "tag": "a", "text": "查看详情", "href": ($line | sub("^查看详情: "; "")) }]
          else
            ([ $line | match("https?://[^ )<>]+"; "g") ]) as $ms
            | if ($ms | length) == 0 then
                [{ "tag": "text", "text": $line }]
              else
                reduce $ms[] as $m
                  ({ out: [], pos: 0 };
                   ($m.offset) as $start
                   | ($m.offset + ($m.string | length)) as $end
                   | ($line[.pos:$start]) as $seg
                   | (if ($seg | test("查看详情: *$")) then { pre: ($seg | sub("查看详情: *$"; "")), label: "查看详情" }
                      elif ($seg | test("报告: *$")) then { pre: ($seg | sub("报告: *$"; "")), label: "查看报告" }
                      else { pre: $seg, label: "查看链接" } end) as $h
                   | .out += (if ($h.pre | length) > 0 then [{ "tag": "text", "text": $h.pre }] else [] end)
                   | .out += [{ "tag": "a", "text": $h.label, "href": $line[$start:$end] }]
                   | .pos = $end)
                | .out + (if .pos < ($line | length) then [{ "tag": "text", "text": $line[.pos:] }] else [] end)
              end
          end;
      ($body | split("\n") | map(select(length > 0))) as $lines
      | [ $lines[] | row ] as $rows
      | { content: { post: { zh_cn: { title: $title, content: $rows } } } }
    ' >"${out_file}"
}

# ── sechook：安全扫描报告全文 ──────────────────────────────────────────
sechook_body() {
  local report="${SECHOOK_REPORT:-sec_overview.md}"
  if [ -f "${report}" ]; then
    cat "${report}"
  else
    printf '读取扫描报告失败: %s\n' "${report}"
  fi
}

render_sechook() {
  local md_file="$1" key="$2" txt_file feishu_file
  txt_file="$(text_path "${md_file}")"
  feishu_file="$(feishu_post_path "${md_file}")"
  LINK_STYLE='md'
  {
    header '安全扫描结果'
    sechook_body
  } | md_linkify >"${md_file}"
  LINK_STYLE='text'
  {
    header '安全扫描结果'
    sechook_body
  } >"${txt_file}"
  build_feishu_post '安全扫描结果' "${txt_file}" "${feishu_file}"
  emit "${md_file}" "${key}"
  emit "${txt_file}" "${key}_text"
}

# ── review：AI 代码评审结果 ────────────────────────────────────────────
review_body() {
  if [ "$(jq '.issues | length' code_review.json)" -gt 0 ]; then
    printf '### 发现 %s 个问题\n\n' "$(jq '.issues | length' code_review.json)"
    # 索引从 1 开始；problem/suggestion 去掉插件自带的 ** 加粗，避免裸 ** 破坏排版
    jq -r '
      .issues | to_entries[] |
      "**\(.key + 1). [\(.value.severity // "info")] \(.value.file // ""):\(.value.start_line // "")**",
      (.value.problem // "" | gsub("\\*\\*"; "") | select(length > 0)),
      (.value.suggestion // "" | gsub("\\*\\*"; "") | select(length > 0)),
      ""
    ' code_review.json
  else
    printf '未发现问题，代码质量良好。\n'
  fi
}

render_review() {
  local md_file="$1" key="$2" txt_file status label title
  [ -f code_review.json ] || die '找不到 code_review.json（code-review 插件需设置 output: ./code_review.json）'

  status="$(jq -r '.status // "unknown"' code_review.json)"
  case "${status}" in
    passed) label='通过' ;;
    needs_modification) label='需要修改' ;;
    critical) label='存在严重问题' ;;
    *) label="${status}" ;;
  esac
  title="AI 代码评审结果: ${label}"
  txt_file="$(text_path "${md_file}")"

  LINK_STYLE='md'
  {
    header "${title}"
    review_body
  } | md_linkify >"${md_file}"
  LINK_STYLE='text'
  {
    header "${title}"
    review_body
  } >"${txt_file}"
  build_feishu_post "${title}" "${txt_file}" "$(feishu_post_path "${md_file}")"
  emit "${md_file}" "${key}"
  emit "${txt_file}" "${key}_text"
}

# ── sca：从 Commit Annotation 读取漏洞/License/SBOM 摘要 ───────────────
# ANNOTATION_LINES 为临时文件，每行格式 |key|value，供 grep -F 精确取值
ANNOTATION_LINES=''

# ann_value <key>：从 annotation 行文件里取指定 key 的 value
ann_value() {
  local key="$1" line
  [ -n "${ANNOTATION_LINES}" ] || return 0
  line="$(grep -F "|${key}|" "${ANNOTATION_LINES}" | head -n 1 || true)"
  printf '%s' "${line#*|}" | cut -d'|' -f2-
}

# has_any <key>...：任一 key 有值即返回 0
has_any() {
  local key
  for key in "$@"; do
    if [ -n "$(ann_value "${key}")" ]; then
      return 0
    fi
  done
  return 1
}

# sca_section <小节标题> <key 前缀> <"字段:显示名 字段:显示名 ...">
sca_section() {
  local title="$1" prefix="$2" fields="$3"
  local pair field label value gate reporter keys=''

  for pair in ${fields}; do
    keys="${keys} ${prefix}${pair%%:*}"
  done
  # shellcheck disable=SC2086  # keys 需按空格拆成多个参数
  if ! has_any ${keys}; then
    return 0
  fi

  printf '### %s\n' "${title}"
  for pair in ${fields}; do
    field="${pair%%:*}"
    label="${pair#*:}"
    value="$(ann_value "${prefix}${field}")"
    if [ -n "${value}" ]; then
      printf -- '- %s: %s\n' "${label}" "${value}"
    fi
  done

  gate="$(ann_value "${prefix}gate")"
  if [ -n "${gate}" ]; then
    if [ "${gate}" = 'gate' ]; then
      gate='命中门禁'
    fi
    printf -- '- 门禁: %s\n' "${gate}"
  fi
  reporter="$(ann_value "${prefix}reporter")"
  if [ -n "${reporter}" ]; then
    printf -- '- 报告: %s\n' "${reporter}"
  fi
  printf '\n'
}

sca_body() {
  local mode="$1" label
  if [ "${mode}" = 'full' ]; then
    label='组件总数'
  else
    label='变化组件数'
  fi
  sca_section '开源组件漏洞' "sca.vulnerability.${mode}." \
    'critical.count:Critical high.count:High medium.count:Medium low.count:Low'
  sca_section 'License 风险' "sca.license.${mode}." \
    'high.count:High medium.count:Medium low.count:Low'
  sca_section 'SBOM 组件' "sca.sbom.${mode}." "total.count:${label}"

  if ! grep -q '^|sca\.' "${ANNOTATION_LINES}" 2>/dev/null; then
    printf '未读取到扫描结果（扫描可能未执行完成）。\n'
  fi
  if [ -n "${SCA_ERROR:-}" ]; then
    printf '读取结果失败: %s\n' "${SCA_ERROR}"
  fi
}

render_sca() {
  local mode="$1" md_file="$2" key="$3"
  local api repo sha token url raw_json txt_file title

  case "${mode}" in
    full) sha="${CNB_COMMIT:-}" ;;
    diff) sha="${CNB_PULL_REQUEST_SHA:-${CNB_COMMIT:-}}" ;;
    *) die "sca 子命令需要 full 或 diff，收到: ${mode}" ;;
  esac

  ANNOTATION_LINES="$(mktemp "${TMPDIR:-/tmp}/certflow-sca.XXXXXX")"
  raw_json="$(mktemp "${TMPDIR:-/tmp}/certflow-sca-json.XXXXXX")"
  SCA_ERROR=''

  api="${CNB_API_ENDPOINT:-}"
  api="${api%/}"
  repo="${CNB_REPO_SLUG:-}"
  token="${CNB_TOKEN:-}"

  if [ -z "${api}" ] || [ -z "${repo}" ] || [ -z "${sha}" ] || [ -z "${token}" ]; then
    SCA_ERROR='缺少 CNB_API_ENDPOINT / CNB_REPO_SLUG / CNB_TOKEN / SHA'
  else
    url="${api}/${repo}/-/git/commit-annotations/${sha}"
    if ! curl -fsS --max-time 30 \
      -H "Authorization: Bearer ${token}" \
      -H 'Accept: application/json' \
      "${url}" -o "${raw_json}" 2>/dev/null; then
      SCA_ERROR='读取 Commit Annotation 失败（OpenAPI 请求错误）'
      : >"${raw_json}"
    fi
  fi

  if [ -s "${raw_json}" ]; then
    jq -r '.[] | select(.key) | "|" + .key + "|" + (.value // "")' "${raw_json}" \
      >"${ANNOTATION_LINES}" 2>/dev/null || : >"${ANNOTATION_LINES}"
  fi
  rm -f "${raw_json}"

  if [ "${mode}" = 'full' ]; then
    title='SCA 全量扫描结果'
  else
    title='SCA 增量扫描结果'
  fi
  txt_file="$(text_path "${md_file}")"

  LINK_STYLE='md'
  {
    header "${title}"
    sca_body "${mode}"
  } | md_linkify >"${md_file}"
  LINK_STYLE='text'
  {
    header "${title}"
    sca_body "${mode}"
  } >"${txt_file}"
  build_feishu_post "${title}" "${txt_file}" "$(feishu_post_path "${md_file}")"

  rm -f "${ANNOTATION_LINES}"
  ANNOTATION_LINES=''
  emit "${md_file}" "${key}"
  emit "${txt_file}" "${key}_text"
}

# ── 自检 ──────────────────────────────────────────────────────────────
self_test() {
  local tmp_dir cwd failures=0 out
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/certflow-notice.XXXXXX")"
  cwd="$(pwd)"

  check() {
    local name="$1" ok="$2" detail="${3:-}"
    if [ "${ok}" = '1' ]; then
      printf '  ✓ %s\n' "${name}"
    else
      printf '  ✗ %s%s\n' "${name}" "$([ -n "${detail}" ] && printf '（%s）' "${detail}")"
      failures=$((failures + 1))
    fi
  }

  printf '时间转换\n'
  CNB_BUILD_START_TIME='2026-11-18T05:45:30.123Z'
  out="$(build_time_text)"
  check 'UTC 05:45:30 -> 北京 13:45:30' "$([ "${out}" = '2026-11-18 13:45:30' ] && printf 1 || printf 0)" "${out}"
  CNB_BUILD_START_TIME='not-a-time'
  out="$(build_time_text)"
  check '非法时间原样返回' "$([ "${out}" = 'not-a-time' ] && printf 1 || printf 0)" "${out}"

  CNB_BUILD_START_TIME='2026-11-18T05:45:30.123Z'
  CNB_REPO_SLUG='dtapp/certflow'
  CNB_PULL_REQUEST_TITLE='feat: test'
  CNB_PULL_REQUEST_PROPOSER='tester'
  CNB_EVENT_URL='https://cnb.cool/dtapp/certflow/-/pulls/1'

  printf 'sechook\n'
  printf '## 敏感信息统计\n高风险: 0\n## 漏洞统计\nCritical: 1\n' >"${tmp_dir}/sec_overview.md"
  SECHOOK_REPORT="${tmp_dir}/sec_overview.md" render_sechook "${tmp_dir}/scan_notice.md" scan >/dev/null
  out="$(cat "${tmp_dir}/scan_notice.md")"
  check '含时间行' "$(printf '%s' "${out}" | grep -q -- '- 时间: 2026-11-18 13:45:30 (UTC+8)' && printf 1 || printf 0)"
  check '含报告原文' "$(printf '%s' "${out}" | grep -q '高风险: 0' && printf 1 || printf 0)"
  check 'md 版超链接' "$(printf '%s' "${out}" | grep -qF '[查看详情](https://cnb.cool/dtapp/certflow/-/pulls/1)' && printf 1 || printf 0)"
  check 'md 版无裸 URL 行' "$(printf '%s' "${out}" | grep -qF '查看详情: https://' && printf 0 || printf 1)"
  out="$(cat "${tmp_dir}/scan_notice.txt")"
  check 'text 版裸 URL' "$(printf '%s' "${out}" | grep -qF '查看详情: https://cnb.cool/dtapp/certflow/-/pulls/1' && printf 1 || printf 0)"
  check 'text 版无 markdown 链接' "$(printf '%s' "${out}" | grep -qF '[查看详情](' && printf 0 || printf 1)"
  check '飞书 post 是合法 JSON' "$(jq -e '.content.post.zh_cn.title == "安全扫描结果"' "${tmp_dir}/scan_notice_feishu.json" >/dev/null 2>&1 && printf 1 || printf 0)"
  check '飞书 post 含 a 标签超链接' "$(jq -e '[.content.post.zh_cn.content[][] | select(.tag == "a" and .text == "查看详情" and .href == "https://cnb.cool/dtapp/certflow/-/pulls/1")] | length > 0' "${tmp_dir}/scan_notice_feishu.json" >/dev/null 2>&1 && printf 1 || printf 0)"

  printf 'review\n'
  cd "${tmp_dir}"
  cat >code_review.json <<'JSON'
{"status":"needs_modification","issues":[{"severity":"warning","file":"main.go","start_line":42,"problem":"**问题**: 缺少校验","suggestion":"**建议**: 增加判空"}]}
JSON
  render_review review_notice.md review >/dev/null
  out="$(cat review_notice.md)"
  cd "${cwd}"
  check '含状态中文' "$(printf '%s' "${out}" | grep -q '需要修改' && printf 1 || printf 0)"
  check '含问题位置' "$(printf '%s' "${out}" | grep -q '\[warning\] main.go:42' && printf 1 || printf 0)"
  check '去掉裸 **' "$(printf '%s' "${out}" | grep -q '问题: 缺少校验' && ! printf '%s' "${out}" | grep -q '\*\*问题\*\*' && printf 1 || printf 0)"
  check 'md 版超链接' "$(printf '%s' "${out}" | grep -qF '[查看详情](https://cnb.cool/dtapp/certflow/-/pulls/1)' && printf 1 || printf 0)"

  printf 'sca\n'
  cat >"${tmp_dir}/annotations.json" <<'JSON'
[
  {"key":"sca.vulnerability.full.high.count","value":"3"},
  {"key":"sca.vulnerability.full.gate","value":"gate"},
  {"key":"sca.vulnerability.full.reporter","value":"https://cnb.cool/report?tab=attachments"},
  {"key":"sca.sbom.full.total.count","value":"128"}
]
JSON
  # 自检不联网：直接把 mock JSON 转成 |key|value 行，跳过 curl
  ANNOTATION_LINES="${tmp_dir}/ann-lines"
  jq -r '.[] | select(.key) | "|" + .key + "|" + (.value // "")' "${tmp_dir}/annotations.json" >"${ANNOTATION_LINES}"
  LINK_STYLE='md'
  {
    header 'SCA 全量扫描结果'
    sca_body full
  } | md_linkify >"${tmp_dir}/sca_notice.md"
  out="$(cat "${tmp_dir}/sca_notice.md")"
  rm -f "${ANNOTATION_LINES}"
  ANNOTATION_LINES=''
  check '含漏洞计数' "$(printf '%s' "${out}" | grep -q 'High: 3' && printf 1 || printf 0)"
  check '门禁映射中文' "$(printf '%s' "${out}" | grep -q '命中门禁' && printf 1 || printf 0)"
  check '含 SBOM 组件数' "$(printf '%s' "${out}" | grep -q '组件总数: 128' && printf 1 || printf 0)"
  check 'md 版超链接' "$(printf '%s' "${out}" | grep -qF '[查看详情](https://cnb.cool/dtapp/certflow/-/pulls/1)' && printf 1 || printf 0)"
  check '报告 URL 已 linkify' "$(printf '%s' "${out}" | grep -qF '[查看报告](https://cnb.cool/report?tab=attachments)' && printf 1 || printf 0)"
  check '不显示完整 URL 作链接文字' "$(printf '%s' "${out}" | grep -qF '[https://' && printf 0 || printf 1)"

  rm -rf "${tmp_dir}"

  if [ "${failures}" -ne 0 ]; then
    printf '\n自检失败: %s 项\n' "${failures}" >&2
    return 1
  fi
  printf '\n自检通过\n'
  return 0
}

usage() {
  cat <<'USAGE'
用法:
  sh scripts/ci/render-ci-notice.sh sechook [输出.md]
  sh scripts/ci/render-ci-notice.sh review  [输出.md]
  sh scripts/ci/render-ci-notice.sh sca full|diff [输出.md]
  sh scripts/ci/render-ci-notice.sh --self-test
USAGE
}

case "${1:-}" in
  sechook) render_sechook "${2:-scan_notice.md}" scan ;;
  review) render_review "${2:-review_notice.md}" review ;;
  sca) render_sca "${2:-}" "${3:-sca_notice.md}" sca ;;
  --self-test) self_test ;;
  -h | --help) usage ;;
  '')
    usage >&2
    exit 2
    ;;
  *) die "未知子命令: ${1}" ;;
esac
