package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenyme/grok2api/backend/internal/pkg/mediafile"
)

// LocalStore 将媒体对象限制在单一根目录内，并使用临时文件与原子硬链接完成提交。
type LocalStore struct {
	root            string
	removeTemporary func(string) error
}

func NewLocalStore(root string) (*LocalStore, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("Direktori storan media tidak sah")
	}
	absolute = filepath.Clean(absolute)
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("Mencipta direktori storan media: %w", err)
	}
	return &LocalStore{root: absolute, removeTemporary: os.Remove}, nil
}

func (s *LocalStore) SaveImage(ctx context.Context, id, mimeType string, data []byte) (string, error) {
	return s.saveObject(ctx, "images", ".image-*", id, mimeType, data, imageExtension)
}

// SaveVideo 将视频对象写入 videos/ 子目录，提交语义与图片一致（原子硬链接、no-replace）。
func (s *LocalStore) SaveVideo(ctx context.Context, id, mimeType string, data []byte) (string, error) {
	return s.saveObject(ctx, "videos", ".video-*", id, mimeType, data, mediafile.VideoExtension)
}

// BeginVideoUpload 创建视频临时文件，供流式限长写入后 CommitVideoUpload 提交。
func (s *LocalStore) BeginVideoUpload(ctx context.Context, id, mimeType string) (tempPath, storageKey string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	extension, ok := mediafile.VideoExtension(mimeType)
	if !ok || len(id) < 2 {
		return "", "", fmt.Errorf("Parameter storan video tidak sah")
	}
	storageKey = filepath.ToSlash(filepath.Join("videos", id[:2], id+extension))
	path, err := s.resolve(storageKey)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", fmt.Errorf("Mencipta direktori video: %w", err)
	}
	// 禁止覆盖已有文件：目标已存在时拒绝新的上传会话。
	if _, statErr := os.Stat(path); statErr == nil {
		return "", "", fmt.Errorf("Objek video sudah wujud")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", statErr
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".video-*")
	if err != nil {
		return "", "", fmt.Errorf("Mencipta fail sementara video: %w", err)
	}
	tempPath = temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		_ = s.removeTemporary(tempPath)
		return "", "", err
	}
	if err := temporary.Close(); err != nil {
		_ = s.removeTemporary(tempPath)
		return "", "", err
	}
	return tempPath, storageKey, nil
}

// CommitVideoUpload 将临时文件原子提交到 storageKey（硬链接 no-replace）。
func (s *LocalStore) CommitVideoUpload(ctx context.Context, tempPath, storageKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.resolve(storageKey)
	if err != nil {
		return err
	}
	// 确保临时文件已落盘。
	// 必须以 O_RDWR 打开：Windows 的 FlushFileBuffers 要求句柄具有写访问权限，
	// 对 O_RDONLY 句柄调用 Sync 会返回 Access is denied；
	// Linux 的 fsync 对只读/读写 fd 行为一致，此举不改变 POSIX 平台语义。
	// O_RDWR 不含 O_TRUNC，不会改动文件内容。
	file, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("Membuka fail sementara video: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("Menyelaras fail video: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(tempPath, path); err != nil {
		return fmt.Errorf("Menyerahkan fail video: %w", err)
	}
	if cleanupErr := s.removeTemporary(tempPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
		slog.Warn("media_temp_cleanup_failed", "path", tempPath, "error", cleanupErr)
	}
	return nil
}

// AbortVideoUpload 清理未提交的临时文件。
func (s *LocalStore) AbortVideoUpload(_ context.Context, tempPath string) error {
	if strings.TrimSpace(tempPath) == "" {
		return nil
	}
	if err := s.removeTemporary(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *LocalStore) saveObject(ctx context.Context, kindDir, tempPattern, id, mimeType string, data []byte, extFn func(string) (string, bool)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	extension, ok := extFn(mimeType)
	if !ok || len(id) < 2 {
		return "", fmt.Errorf("Parameter storan media tidak sah")
	}
	storageKey := filepath.ToSlash(filepath.Join(kindDir, id[:2], id+extension))
	path, err := s.resolve(storageKey)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("Mencipta direktori media: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), tempPattern)
	if err != nil {
		return "", fmt.Errorf("Mencipta fail sementara media: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanupPending := true
	defer func() {
		_ = temporary.Close()
		if cleanupPending {
			if cleanupErr := s.removeTemporary(temporaryPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				slog.Warn("media_temp_cleanup_failed", "path", temporaryPath, "error", cleanupErr)
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := temporary.Write(data); err != nil {
		return "", fmt.Errorf("Menulis media: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("Menyelaras fail media: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("Menutup fail media: %w", err)
	}
	// 硬链接提交具有 no-replace 语义，极端 ID 冲突时不会覆盖已有对象。
	if err := os.Link(temporaryPath, path); err != nil {
		return "", fmt.Errorf("Menyerahkan fail media: %w", err)
	}
	cleanupErr := s.removeTemporary(temporaryPath)
	cleanupPending = cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist)
	return storageKey, nil
}

func (s *LocalStore) Open(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.resolve(storageKey)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("Membuka fail media: %w", err)
	}
	return file, nil
}

func (s *LocalStore) Delete(ctx context.Context, storageKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.resolve(storageKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("Memadam fail media: %w", err)
	}
	return nil
}

func (s *LocalStore) resolve(storageKey string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(storageKey)))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Laluan storan media tidak sah")
	}
	full := filepath.Join(s.root, clean)
	relative, err := filepath.Rel(s.root, full)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Laluan storan media melampaui had")
	}
	return full, nil
}

func imageExtension(mimeType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	case "image/gif":
		return ".gif", true
	default:
		return "", false
	}
}
