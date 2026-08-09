package file

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/rest"
	"github.com/bilirec/bilirec/internal/services/convert"
	"github.com/bilirec/bilirec/internal/services/danmaku"
	"github.com/bilirec/bilirec/internal/services/file"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("controller", "file")

type Controller struct {
	fileSvc     *file.Service
	convertSvc  *convert.Service
	recorderSvc *recorder.Service
	pathSvc     *path.Service
}

func NewController(
	app *fiber.App,
	fileSvc *file.Service,
	recorderSvc *recorder.Service,
	pathSvc *path.Service,
	convertSvc *convert.Service,
) *Controller {
	fc := &Controller{
		fileSvc:     fileSvc,
		recorderSvc: recorderSvc,
		pathSvc:     pathSvc,
		convertSvc:  convertSvc,
	}
	files := app.Group("/files")

	files.Get("/browse/*", fc.listFiles)
	files.Get("/playback/*", fc.playbackFile)
	files.Get("/danmaku/*", fc.fetchDanmaku)
	files.Get("/download/*", fc.downloadFile)
	files.Get("/tempdownload", fc.presignedDownload)
	files.Get("/disk-space", fc.getDiskSpace)
	files.Post("/presigned/*", fc.createPresignedURL)

	files.Delete("/batch", rest.AdminOnly, fc.deleteFiles)
	files.Delete("/*", rest.AdminOnly, fc.deleteDir)

	return fc
}

// @Summary Playback a video file
// @Description Stream a video file inline for browser playback (VOD only)
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce video/mp4
// @Param path path string true "Video file path"
// @Success 200 {file} binary "Video stream"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Failure 415 {string} string "Unsupported media type"
// @Router /files/playback/{path} [get]
func (c *Controller) playbackFile(ctx fiber.Ctx) error {
	raw := ctx.Params("*", "/")
	path, err := url.PathUnescape(raw)
	if err != nil {
		return fiber.ErrBadRequest
	} else if c.recorderSvc.IsRecording(path) {
		return fiber.NewError(fiber.StatusBadRequest, "无法播放正在录制的文件")
	}

	fullPath, mimeType, err := c.fileSvc.OpenForPlayback(path)
	if err != nil {
		logger.Warnf("打开播放文件 %s 失败：%v", path, err)
		return c.parseFiberError(err)
	}

	ctx.Set(fiber.HeaderContentType, mimeType)
	ctx.Set(
		fiber.HeaderContentDisposition,
		"inline; filename=\""+filepath.Base(fullPath)+"\"",
	)

	return c.sendFileWithIdleCacheRelease(ctx, fullPath, fiber.SendFile{ByteRange: true})
}

// @Summary Fetch danmaku for a video
// @Description Stream the danmaku sidecar paired with a video segment (.jsonl preferred, then .xml)
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce application/x-ndjson
// @Produce application/xml
// @Param path path string true "Video file path"
// @Success 200 {file} binary "Danmaku stream"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /files/danmaku/{path} [get]
func (c *Controller) fetchDanmaku(ctx fiber.Ctx) error {
	raw := ctx.Params("*", "/")
	videoPath, err := url.PathUnescape(raw)
	if err != nil {
		return fiber.ErrBadRequest
	}
	if c.recorderSvc.IsRecording(videoPath) {
		return fiber.NewError(fiber.StatusBadRequest, "无法获取正在录制分段的弹幕")
	}

	var fullPath string
	for _, cand := range danmaku.SidecarCandidates(videoPath) {
		fp, err := c.pathSvc.ValidatePath(cand)
		if err != nil {
			continue
		}
		st, err := os.Stat(fp)
		if err != nil || st.IsDir() {
			continue
		}
		fullPath = fp
		break
	}
	if fullPath == "" {
		return fiber.NewError(fiber.StatusNotFound, "弹幕文件不存在")
	}

	contentType := "application/x-ndjson; charset=utf-8"
	if strings.EqualFold(filepath.Ext(fullPath), ".xml") {
		contentType = "application/xml; charset=utf-8"
	}
	ctx.Set(fiber.HeaderContentType, contentType)
	ctx.Set(
		fiber.HeaderContentDisposition,
		"inline; filename=\""+filepath.Base(fullPath)+"\"",
	)
	return c.sendFileWithIdleCacheRelease(ctx, fullPath, fiber.SendFile{ByteRange: true})
}

func (c *Controller) sendFileWithIdleCacheRelease(ctx fiber.Ctx, fullPath string, cfg fiber.SendFile) error {
	done := c.fileSvc.BeginServeCacheRelease(fullPath)
	defer done()
	return ctx.SendFile(fullPath, cfg)
}

// @Summary List files and directories
// @Description List files and directories under a given path with optional pagination and search
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param path path string false "Relative path"
// @Param offset query int false "Offset (default 0)"
// @Param limit query int false "Limit (default 0 = all, max 200)"
// @Param search query string false "Search by filename (case-insensitive)"
// @Success 200 {object} file.PagedTree "Paged list of files and directories"
// @Failure 400 {string} string "Invalid path"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /files/browse/{path} [get]
func (c *Controller) listFiles(ctx fiber.Ctx) error {
	raw := ctx.Params("*", "/")
	path, err := url.PathUnescape(raw)
	if err != nil {
		return fiber.ErrBadRequest
	}

	offset, err := strconv.Atoi(ctx.Query("offset", "0"))
	if err != nil || offset < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "offset 必须为非负整数")
	}
	limit, err := strconv.Atoi(ctx.Query("limit", "0"))
	if err != nil || limit < 0 || limit > 200 {
		return fiber.NewError(fiber.StatusBadRequest, "limit 必须为 0 至 200 之间的整数")
	}

	paged, err := c.fileSvc.ListTreeWithOptions(path, file.ListOptions{
		Filter: func(f fs.DirEntry) bool {
			return !strings.HasSuffix(f.Name(), ".tmp")
		},
		Search: ctx.Query("search"),
		Offset: offset,
		Limit:  limit,
	})
	if err != nil {
		logger.Warnf("列出路径 %s 的目录失败：%v", path, err)
		return c.parseFiberError(err)
	}
	paged.Items = c.withRecordingStatus(paged.Items)
	return ctx.JSON(paged)
}

// @Summary Download a file
// @Description Download a file or convert it to the requested format
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce octet-stream
// @Param path path string true "File path"
// @Success 200 {file} binary "File stream"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /files/download/{path} [get]
func (c *Controller) downloadFile(ctx fiber.Ctx) error {
	raw := ctx.Params("*", "/")
	path, err := url.PathUnescape(raw)
	if err != nil {
		return fiber.ErrBadRequest
	}
	if c.recorderSvc.IsRecording(path) {
		return fiber.NewError(fiber.StatusBadRequest, "无法下载正在录制的文件")
	}
	fullPath, err := c.pathSvc.ValidatePath(path)
	if err != nil {
		logger.Warnf("校验路径 %s 失败：%v", path, err)
		return c.parseFiberError(err)
	}
	ctx.Attachment(fullPath) // set this because SendFile does not set the filename when using Download: true
	return c.sendFileWithIdleCacheRelease(ctx, fullPath, fiber.SendFile{
		ByteRange: true,
	})
}

// @Summary Presigned download
// @Description Download a file using a presigned token (no auth required)
// @Tags files
// @Accept json
// @Produce octet-stream
// @Param presigned query string true "Presigned URL token"
// @Success 200 {file} binary "File stream"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Token expired"
// @Failure 404 {string} string "Not found"
// @Router /files/tempdownload [get]
func (c *Controller) presignedDownload(ctx fiber.Ctx) error {
	token := ctx.Query("presigned", "")
	if token == "" {
		return fiber.ErrBadRequest
	}
	relPath, err := c.pathSvc.ParsePresignedURLToken(token)
	if err != nil {
		if err == path.ErrTokenExpired {
			return fiber.NewError(fiber.StatusForbidden, "Token 已过期")
		}
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	fullPath, err := c.pathSvc.ValidatePath(relPath)
	if err != nil {
		logger.Warnf("校验路径 %s 失败：%v", relPath, err)
		return c.parseFiberError(err)
	}
	ctx.Attachment(fullPath) // set this because SendFile does not set the filename when using Download: true
	return c.sendFileWithIdleCacheRelease(ctx, fullPath, fiber.SendFile{
		ByteRange: true,
	})
}

// @Summary Create presigned URL
// @Description Create a presigned token for downloading a file. Accepts optional "ttl" query in seconds (default 3600).
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param path path string true "File path"
// @Param ttl query int false "TTL in seconds"
// @Success 201 {object} PresignedURLResponse "Presigned URL response"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /files/presigned/{path} [post]
func (c *Controller) createPresignedURL(ctx fiber.Ctx) error {
	raw := ctx.Params("*", "/")
	path, err := url.PathUnescape(raw)
	if err != nil {
		return fiber.ErrBadRequest
	} else if c.recorderSvc.IsRecording(path) {
		return fiber.NewError(fiber.StatusBadRequest, "无法为正在录制的文件生成临时下载链接")
	}

	fullPath, err := c.pathSvc.ValidatePath(path)
	if err != nil {
		logger.Warnf("校验路径 %s 失败：%v", path, err)
		return c.parseFiberError(err)
	}

	ttlStr := ctx.Query("ttl", "")
	ttlSeconds := int64(3600)
	if ttlStr != "" {
		n, err := strconv.ParseInt(ttlStr, 10, 64)
		if err != nil || n <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "ttl 无效")
		}
		ttlSeconds = n
	}
	ttl := time.Duration(ttlSeconds) * time.Second

	url, err := c.pathSvc.GeneratePresignedURL(fullPath, ttl)
	if err != nil {
		logger.Warnf("为路径 %s 生成预签名令牌失败：%v", path, err)
		return fiber.ErrInternalServerError
	}

	resp := &PresignedURLResponse{
		URL:       url,
		ExpiresIn: int(ttl.Seconds()),
	}
	return ctx.Status(fiber.StatusCreated).JSON(resp)
}

// @Summary Get disk space
// @Description Get disk space usage information for the output directory
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} file.DiskSpace "Disk space information"
// @Failure 500 {string} string "Internal server error"
// @Router /files/disk-space [get]
func (c *Controller) getDiskSpace(ctx fiber.Ctx) error {
	space, err := c.fileSvc.GetDiskSpace()
	if err != nil {
		logger.Warnf("获取磁盘空间失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(space)
}

// @Summary Delete multiple files
// @Description Delete multiple files by their relative paths
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param paths body []string true "List of relative file paths to delete"
// @Success 204 "No Content"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /files/batch [delete]
func (c *Controller) deleteFiles(ctx fiber.Ctx) error {
	var paths []string
	if err := ctx.Bind().Body(&paths); err != nil {
		return fiber.ErrBadRequest
	}

	for _, p := range paths {
		if c.recorderSvc.IsRecording(p) {
			return fiber.NewError(fiber.StatusBadRequest, "要删除的文件中包含正在录制的文件")
		} else if fullPath, err := c.pathSvc.ValidatePath(p); err != nil {
			logger.Warnf("校验路径 %s 失败：%v", p, err)
			return c.parseFiberError(err)
		} else if inQueue, err := c.convertSvc.IsInQueue(fullPath); err != nil && err != convert.ErrNoConvertManager {
			logger.Warnf("检查路径 %s 的转码队列失败：%v", p, err)
			return fiber.ErrInternalServerError
		} else if inQueue {
			return fiber.NewError(fiber.StatusBadRequest, "要删除的文件中包含正在转码的文件")
		}
	}

	if err := c.fileSvc.DeleteFiles(paths...); err != nil {
		logger.Warnf("删除文件失败：%v", err)
		return c.parseFiberError(err)
	}
	return ctx.SendStatus(fiber.StatusNoContent)
}

// @Summary Delete a directory
// @Description Delete a directory and all its contents
// @Tags files
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param path path string true "Directory path"
// @Success 204 "No Content"
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /files/{path} [delete]
func (c *Controller) deleteDir(ctx fiber.Ctx) error {
	raw := ctx.Params("*", "/")
	path, err := url.PathUnescape(raw)
	if err != nil {
		return fiber.ErrBadRequest
	} else if c.recorderSvc.IsRecordingUnder(path) {
		return fiber.NewError(fiber.StatusBadRequest, "无法删除包含正在录制文件的文件夹")
	} else if err := c.fileSvc.DeleteDirectory(path); err != nil {
		logger.Warnf("删除路径 %s 的目录失败：%v", path, err)
		return c.parseFiberError(err)
	}
	return ctx.SendStatus(fiber.StatusNoContent)
}

func (c *Controller) parseFiberError(err error) error {
	switch {
	case os.IsNotExist(err):
		return fiber.NewError(fiber.StatusNotFound, "找不到所属文件夹或文件")
	case os.IsPermission(err), err == path.ErrAccessDenied:
		return fiber.NewError(fiber.StatusForbidden, "无法访问该文件路径")
	case err == path.ErrInvalidFilePath:
		return fiber.NewError(fiber.StatusBadRequest, "无效文件路径")
	case err == file.ErrIsDirectory:
		return fiber.NewError(fiber.StatusBadRequest, "该路径是文件夹")
	case err == file.ErrUnsupportedPlaybackMedia:
		return fiber.NewError(fiber.StatusUnsupportedMediaType, "该文件格式不支持在线播放")
	default:
		return fiber.ErrInternalServerError
	}
}

func (c *Controller) withRecordingStatus(items []file.Tree) []file.Tree {
	out := make([]file.Tree, len(items))
	copy(out, items)
	for i := range out {
		if !out[i].IsDir {
			out[i].IsRecording = c.recorderSvc.IsRecording(out[i].Path)
		}
	}
	return out
}
