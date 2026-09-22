// Package mbid 实现「本地音乐文件标签 → 飞牛曲目 GUID」的 MBID 解析与落库。
//
// 思路：
//  1. 遍历 library.directories 下的音频文件；
//  2. 用 dhowden/tag 解析 ID3/FLAC/M4A/OGG 等标签，提取 MusicBrainz ID
//     （录音/专辑/艺人/作品 MBID）以及曲名、艺人、专辑；
//  3. 按「艺人+曲名」（含主标题降级）、「唯一曲名」、再兜底「文件名」匹配到飞牛曲目 GUID；
//  4. 以 feiniu_guid 为主键写入 track_mbid_map（幂等）。
//
// 落库的 MBID 再由 EnrichIndexes 注入飞牛 TrackIndexes.MBIDToGUID，
// 让 scrobble / 歌单匹配优先走精确的录音 MBID（优于「艺人名+曲名」模糊匹配）。
package mbid

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/db"
	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
	"github.com/dhowden/tag"
)

// audioExts 支持的音频文件扩展名（仅这些才会尝试解析标签）。
var audioExts = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".mp4": true,
	".ogg": true, ".opus": true, ".wav": true, ".aiff": true,
	".ape": true, ".wv": true, ".dsf": true, ".wma": true,
}

// ScanStats 一次扫描的统计结果（同时作为 Web UI 的返回结构）。
type ScanStats struct {
	Scanned   int    `json:"scanned"`    // 解析过的音频文件数
	Matched   int    `json:"matched"`    // 成功匹配到飞牛曲目的文件数
	Updated   int    `json:"updated"`    // 写入/更新了 MBID 映射的行数
	Skipped   int    `json:"skipped"`    // 无 MBID 或匹配失败跳过
	Errors    int    `json:"errors"`     // 解析/读文件出错数
	ElapsedMs int64  `json:"elapsed_ms"` // 耗时（毫秒）
	Message   string `json:"message"`    // 摘要信息
}

// TrackLister 拉取全部飞牛曲目（需要一个有效 token 的客户端）。
// feiniu.Client 实现了该签名（内部自行分页）。
type TrackLister func(ctx context.Context) ([]feiniu.Track, error)

// Resolver 扫描本地音乐目录、解析标签，把 MBID 关联到飞牛曲目 GUID 并落库。
type Resolver struct {
	cfg    *config.Config
	store  *db.Store
	lister TrackLister
	logger *slog.Logger
}

// NewResolver 构造解析器。
// lister 用于拉取飞牛曲库（GUID、路径、元数据），由调用方注入（需要有效 token）。
func NewResolver(cfg *config.Config, store *db.Store, lister TrackLister, logger *slog.Logger) *Resolver {
	return &Resolver{cfg: cfg, store: store, lister: lister, logger: logger}
}

// Scan 执行一次完整扫描：遍历目录 → 解析标签 → 匹配飞牛曲目 → 落库。
// 任何单项错误（解析失败/落库失败）都不会中断整体扫描。
func (r *Resolver) Scan(ctx context.Context) (ScanStats, error) {
	start := time.Now()
	stats := ScanStats{}

	if len(r.cfg.Library.Directories) == 0 {
		stats.Message = "未配置 library.directories，跳过 MBID 扫描"
		stats.ElapsedMs = time.Since(start).Milliseconds()
		return stats, nil
	}
	if r.lister == nil {
		stats.Message = "未配置飞牛客户端，跳过 MBID 扫描"
		stats.ElapsedMs = time.Since(start).Milliseconds()
		return stats, nil
	}

	tracks, err := r.lister(ctx)
	if err != nil {
		stats.Message = "拉取飞牛曲库失败: " + err.Error()
		stats.ElapsedMs = time.Since(start).Milliseconds()
		return stats, err
	}
	if len(tracks) == 0 {
		stats.Message = "飞牛曲库为空，跳过 MBID 扫描"
		stats.ElapsedMs = time.Since(start).Milliseconds()
		return stats, nil
	}

	byArtistTitle, byTitle, byBase := buildTrackIndex(tracks)
	r.logger.Info("MBID 扫描：已建立飞牛曲目匹配索引",
		"曲目数", len(tracks),
		"艺人+曲名", len(byArtistTitle),
		"唯一曲名", len(byTitle),
		"路径基名", len(byBase),
	)

	for _, dir := range r.cfg.Library.Directories {
		absDir, aerr := filepath.Abs(dir)
		if aerr != nil {
			stats.Errors++
			r.logger.Warn("MBID 扫描：目录绝对路径解析失败", "目录", dir, "错误", aerr)
			continue
		}

		walkErr := filepath.WalkDir(absDir, func(path string, d os.DirEntry, werr error) error {
			if werr != nil {
				// 跳过无法访问的子目录/文件，不中断整体遍历。
				return nil //nolint:nilerr // 故意：遇到不可读项跳过而非中断遍历
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				return nil
			}
			if !audioExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}

			stats.Scanned++
			m, perr := parseFile(path)
			if perr != nil {
				stats.Errors++
				return nil //nolint:nilerr // 故意：解析失败的单文件跳过，不中断整体扫描
			}

			guid := matchTrack(byArtistTitle, byTitle, byBase, m, path)
			if guid == "" {
				stats.Skipped++
				return nil
			}
			stats.Matched++

			// 没有任何 MBID 时跳过落库（本表核心是记录 MBID）。
			if isZeroMBID(m) {
				stats.Skipped++
				return nil
			}

			rec := db.TrackMBID{
				FeiniuGUID:       guid,
				FilePath:         path,
				Title:            m.Title,
				Artist:           m.Artist,
				Album:            m.Album,
				RecordingMBID:    m.RecordingMBID,
				ReleaseMBID:      m.ReleaseMBID,
				ReleaseGroupMBID: m.ReleaseGroupMBID,
				ArtistMBID:       m.ArtistMBID,
				WorkMBID:         m.WorkMBID,
				MatchedBy:        "tag",
			}
			if serr := r.store.SaveTrackMBID(rec); serr != nil {
				stats.Errors++
				r.logger.Warn("MBID 保存失败", "路径", path, "错误", serr)
				return nil
			}
			stats.Updated++
			return nil
		})
		if walkErr != nil {
			stats.Errors++
			r.logger.Warn("MBID 扫描：遍历目录出错", "目录", absDir, "错误", walkErr)
		}
	}

	stats.Message = "扫描完成"
	stats.ElapsedMs = time.Since(start).Milliseconds()
	r.logger.Info("MBID 扫描完成",
		"扫描", stats.Scanned, "匹配", stats.Matched, "更新", stats.Updated,
		"跳过", stats.Skipped, "错误", stats.Errors, "耗时(ms)", stats.ElapsedMs,
	)
	return stats, nil
}

// fileMeta 单个音频文件解析出的标签信息。
type fileMeta struct {
	Title, Artist, Album                         string
	RecordingMBID, ReleaseMBID, ReleaseGroupMBID string
	ArtistMBID, WorkMBID                         string
}

// isZeroMBID 判断是否有任意一个 MBID 字段非空。
func isZeroMBID(m fileMeta) bool {
	return m.RecordingMBID == "" && m.ReleaseMBID == "" &&
		m.ReleaseGroupMBID == "" && m.ArtistMBID == "" && m.WorkMBID == ""
}

// parseFile 打开音频文件并用 dhowden/tag 解析标签。
func parseFile(path string) (fileMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileMeta{}, err
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return fileMeta{}, err
	}
	return extractMeta(m), nil
}

// extractMeta 从 tag.Metadata 提取曲名/艺人/专辑与各类 MusicBrainz ID。
// MBID 来自标签中的 MusicBrainz 字段：
//   - ID3v2：TXXX 用户文本帧，原始值类型为 *tag.Comm（Description 即字段名，Text 即值）；
//   - Vorbis/FLAC/MP4：原始键名即字段名（如 MUSICBRAINZ_RECORDINGID）。
func extractMeta(m tag.Metadata) fileMeta {
	fm := fileMeta{
		Title:  strings.TrimSpace(m.Title()),
		Artist: strings.TrimSpace(m.Artist()),
		Album:  strings.TrimSpace(m.Album()),
	}

	for k, v := range m.Raw() {
		name, val := normalizeRawEntry(k, v)
		if name == "" || val == "" {
			continue
		}
		switch classifyMBID(name) {
		case "recording":
			if fm.RecordingMBID == "" {
				fm.RecordingMBID = val
			}
		case "releasegroup":
			if fm.ReleaseGroupMBID == "" {
				fm.ReleaseGroupMBID = val
			}
		case "release":
			if fm.ReleaseMBID == "" {
				fm.ReleaseMBID = val
			}
		case "artist":
			if fm.ArtistMBID == "" {
				fm.ArtistMBID = val
			}
		case "work":
			if fm.WorkMBID == "" {
				fm.WorkMBID = val
			}
		}
	}
	return fm
}

// normalizeRawEntry 把 Raw() 中的一个条目规范成 (字段名, 值)。
// 返回值都为空串表示无法识别，应忽略。
func normalizeRawEntry(k string, v any) (string, string) {
	switch val := v.(type) {
	case *tag.Comm:
		// ID3v2 TXXX 帧：Description 是字段名（如 "MusicBrainz Recording Id"）。
		return strings.ToLower(strings.TrimSpace(val.Description)), strings.TrimSpace(val.Text)
	case string:
		// Vorbis/FLAC/MP4：键名即字段名（如 "MUSICBRAINZ_RECORDINGID"）。
		return strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(val)
	case fmt.Stringer:
		return strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(val.String())
	default:
		return "", ""
	}
}

// classifyMBID 把 MusicBrainz 标签字段名归并为五类之一（返回空串表示非 MBID 字段）。
// 顺序很重要：先匹配更具体的词（releasegroup 先于 release，albumartist 先于 artist）。
func classifyMBID(name string) string {
	if !strings.Contains(name, "musicbrainz") && !strings.Contains(name, "mbid") {
		return ""
	}
	switch {
	case strings.Contains(name, "releasegroup"):
		return "releasegroup"
	case strings.Contains(name, "release"):
		return "release"
	case strings.Contains(name, "recording"):
		return "recording"
	case strings.Contains(name, "track"):
		// MusicBrainz Track Id 也视作录音匹配候选（兜底）。
		return "recording"
	case strings.Contains(name, "albumartist"):
		return "artist"
	case strings.Contains(name, "artist"):
		return "artist"
	case strings.Contains(name, "work"):
		return "work"
	}
	return ""
}

// buildTrackIndex 由飞牛曲目列表构建三种匹配索引（与 feiniu.BuildTrackIndexes 同口径）：
//   - byArtistTitle：艺人名+曲名（含主标题降级）→ GUID
//   - byTitle：仅曲名（曲库内唯一时）→ GUID
//   - byBase：文件名（不含目录）→ []GUID（路径兜底，仅当唯一时采用）
func buildTrackIndex(tracks []feiniu.Track) (byArtistTitle, byTitle map[string]string, byBase map[string][]string) {
	byArtistTitle = make(map[string]string)
	byTitle = make(map[string]string)
	byBase = make(map[string][]string)
	titleCandidates := make(map[string][]string)

	for _, t := range tracks {
		artist := ""
		if len(t.Artists) > 0 {
			artist = t.Artists[0].Name
		}
		if artist != "" && t.Title != "" {
			byArtistTitle[feiniu.BuildArtistTrackKey(artist, t.Title)] = t.GUID
			if main := feiniu.MainTitle(t.Title); main != t.Title {
				byArtistTitle[feiniu.BuildArtistTrackKey(artist, main)] = t.GUID
			}
		}
		if t.Title != "" {
			key := feiniu.NormalizeForMatch(t.Title)
			titleCandidates[key] = append(titleCandidates[key], t.GUID)
			if mainKey := feiniu.NormalizeForMatch(feiniu.MainTitle(t.Title)); mainKey != key {
				titleCandidates[mainKey] = append(titleCandidates[mainKey], t.GUID)
			}
		}
		if base := strings.ToLower(filepath.Base(t.AudioSpec.Path)); base != "" {
			byBase[base] = append(byBase[base], t.GUID)
		}
	}

	for title, guids := range titleCandidates {
		if len(guids) == 1 {
			byTitle[title] = guids[0]
		}
	}
	return byArtistTitle, byTitle, byBase
}

// matchTrack 在飞牛索引里查找文件对应的曲目 GUID，按精度从高到低依次尝试：
//  1. 艺人名 + 曲名（含主标题降级）
//  2. 仅曲名（曲库内曲名唯一时）
//  3. 文件名完全一致且唯一（跨挂载路径不可靠时的兜底）
func matchTrack(byArtistTitle, byTitle map[string]string, byBase map[string][]string, m fileMeta, path string) string {
	if m.Artist != "" && m.Title != "" {
		if g, ok := byArtistTitle[feiniu.BuildArtistTrackKey(m.Artist, m.Title)]; ok {
			return g
		}
		if main := feiniu.MainTitle(m.Title); main != m.Title {
			if g, ok := byArtistTitle[feiniu.BuildArtistTrackKey(m.Artist, main)]; ok {
				return g
			}
		}
	}
	if m.Title != "" {
		if g, ok := byTitle[feiniu.NormalizeForMatch(m.Title)]; ok {
			return g
		}
		if main := feiniu.MainTitle(m.Title); main != m.Title {
			if g, ok := byTitle[feiniu.NormalizeForMatch(main)]; ok {
				return g
			}
		}
	}
	if base := strings.ToLower(filepath.Base(path)); base != "" {
		if guids, ok := byBase[base]; ok && len(guids) == 1 {
			return guids[0]
		}
	}
	return ""
}
