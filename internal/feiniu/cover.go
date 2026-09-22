package feiniu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ResolvePlaylistCover 由曲目 coverId 得到可用于歌单封面的 playlist 类型 coverId。
//
// 直接把曲目 coverId（如 album_xxx）套到歌单封面上并不显示，
// 飞牛歌单封面只认 playlist 类型的封面资源。因此这里：下载曲目封面 → 上传为歌单封面
// → 拿到 playlist_xxx coverId。
//
// 飞牛音乐对上传的封面做了去重（相同图片复用同一 coverId），因此无需在本地缓存
// 下载/上传结果，每次同步直接重新下载+上传即可，不会产生重复存储。
//
// 任一步骤失败都返回 ("", nil)，由调用方回退到默认封面，不中断歌单同步。
func (c *Client) ResolvePlaylistCover(ctx context.Context, trackCoverId string) (string, error) {
	if trackCoverId == "" {
		return "", nil
	}

	img, err := c.downloadCoverBytes(ctx, trackCoverId, 600)
	if err != nil {
		c.logger.Warn("下载曲目封面失败，回退默认封面",
			"coverId", trackCoverId, "错误", err)
		return "", nil
	}

	plCoverId, err := c.uploadPlaylistCover(ctx, img)
	if err != nil {
		c.logger.Warn("上传歌单封面失败，回退默认封面",
			"coverId", trackCoverId, "错误", err)
		return "", nil
	}

	return plCoverId, nil
}

// downloadCoverBytes 下载指定 coverId 的封面图片字节。
// size 为请求尺寸（px），<=0 时回退 600（尽量取高清，避免上传后放大发糊）。
//
// 下载后会校验字节是否为有效图片：飞牛的封面接口在 coverId 失效或出错时，
// 可能返回 JSON 错误页 / HTML 而非图片。若不校验，会把垃圾字节当作封面上传。
func (c *Client) downloadCoverBytes(ctx context.Context, coverId string, size int) ([]byte, error) {
	if size <= 0 {
		size = 600
	}
	path := fmt.Sprintf("/music/api/v1/static/cover?coverId=%s&size=%d", url.QueryEscape(coverId), size)

	data, err := c.getBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	if !isLikelyImage(data) {
		return nil, fmt.Errorf("下载到的封面不是有效图片（长度 %d，可能不是图片而是错误响应）", len(data))
	}
	c.logger.Debug("下载曲目封面成功",
		"coverId", coverId, "格式", extFromData(data), "字节数", len(data))
	return data, nil
}

// isLikelyImage 按文件头签名判断字节是否看起来是常见图片格式。
// 用于下载封面后的校验，避免把错误页 / JSON 等非法响应当作图片上传。
func isLikelyImage(data []byte) bool {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF: // JPEG
		return true
	case len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47: // PNG
		return true
	case len(data) >= 3 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46: // GIF
		return true
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")): // WEBP
		return true
	case len(data) >= 2 && data[0] == 0x42 && data[1] == 0x4D: // BMP
		return true
	}
	return false
}

// uploadPlaylistCover 上传图片字节作为歌单封面，返回 playlist 类型的 coverId。
//
//	POST /music/api/v1/static/cover/playlist
//	body: multipart/form-data，字段名 file
//	响应: {"code":0,"msg":"","data":{"coverId":"playlist_xxx"}}
//
// 底层 multipart 请求由 client.uploadMultipart 完成（通用方法，不绑定封面业务），
// 此处仅负责封面接口的端点、字段名与响应解析。
func (c *Client) uploadPlaylistCover(ctx context.Context, data []byte) (string, error) {
	respBytes, err := c.uploadMultipart(ctx,
		"/music/api/v1/static/cover/playlist", "file", "cover."+extFromData(data), data)
	if err != nil {
		return "", fmt.Errorf("上传封面请求失败: %w", err)
	}

	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			CoverId string `json:"coverId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("解析上传响应失败: %w", err)
	}
	if out.Code != 0 {
		return "", fmt.Errorf("上传封面失败 code=%d msg=%s", out.Code, out.Msg)
	}
	if out.Data.CoverId == "" {
		return "", fmt.Errorf("上传封面返回空 coverId")
	}
	return out.Data.CoverId, nil
}

// extFromData 根据图片文件头（magic bytes）猜测扩展名，用于上传时的文件名。
//
// 注意：这里比较的是原始字节（文件签名），不是文本，因此不存在大小写问题——
// 各图片格式的文件头是固定的二进制常量（如 JPEG 恒为 FF D8 FF，GIF 恒为
// 47 49 46 即 "GIF"，WEBP 恒为 "RIFF"…"WEBP"），格式规范里就只有这一种写法，
// 不会因为大小写不同而匹配不到。
func extFromData(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "jpg"
	case len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return "png"
	case len(data) >= 3 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
		return "gif"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "webp"
	case len(data) >= 2 && data[0] == 0x42 && data[1] == 0x4D: // BMP: "BM"
		return "bmp"
	default:
		// 无法识别时回退 jpg：仅是 multipart 文件名的提示，服务端按实际字节解码。
		return "jpg"
	}
}
