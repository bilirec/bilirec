package path

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/pkg/signeddownload"
	"github.com/eric2788/bilirec/utils"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("service", "path")

var (
	ErrFileNotFound    = fmt.Errorf("file not found")
	ErrInvalidFilePath = fmt.Errorf("invalid file path")
	ErrAccessDenied    = fmt.Errorf("access denied")
	ErrTokenExpired    = fmt.Errorf("token expired")
	ErrInvalidToken    = fmt.Errorf("invalid token")
)

type Service struct {
	cfg       *config.Config
	presigner *signeddownload.Client
}

func NewService(cfg *config.Config) *Service {
	return &Service{
		cfg:       cfg,
		presigner: signeddownload.NewClient([]byte(cfg.JwtSecret)),
	}
}

func (s *Service) GeneratePresignedURL(fullPath string, expireAfter time.Duration) (string, error) {
	relPath, err := s.GetRelativePath(fullPath)
	if err != nil {
		return "", err
	}
	token, err := s.presigner.GenerateDownloadToken(relPath, time.Now().Add(expireAfter).Unix())
	if err != nil {
		return "", err
	}

	fallbackURL := fmt.Sprintf("/files/tempdownload?presigned=%s", token)
	baseURL := strings.TrimSpace(s.cfg.PublicBaseUrl)
	if baseURL == "" {
		logger.Warn("PUBLIC_BASE_URL is empty, returning relative presigned URL")
		return fallbackURL, nil
	}

	if !utils.IsValidAbsoluteHTTPURL(baseURL) {
		logger.Warnf("PUBLIC_BASE_URL is invalid: %q, returning relative presigned URL", s.cfg.PublicBaseUrl)
		return fallbackURL, nil
	}

	return fmt.Sprintf("%s/files/tempdownload?presigned=%s", strings.TrimRight(baseURL, "/"), token), nil
}

func (s *Service) ParsePresignedURL(fullURL string) (string, error) {
	u, err := url.Parse(fullURL)
	if err != nil {
		return "", err
	}
	token := u.Query().Get("presigned")
	if token == "" {
		return "", ErrInvalidToken
	}
	return s.ParsePresignedURLToken(token)
}

func (s *Service) ParsePresignedURLToken(token string) (string, error) {
	claim, err := s.presigner.ParseDownloadToken(token)
	if err != nil {
		return "", ErrInvalidToken
	}
	if time.Now().Unix() > claim.Exp {
		return "", ErrTokenExpired
	}
	return claim.FilePath, nil
}

func (s *Service) ValidatePath(path string) (string, error) {
	baseAbs, err := filepath.Abs(s.cfg.OutputDir)
	if err != nil {
		logger.Errorf("invalid base path for %s: %v", s.cfg.OutputDir, err)
		return "", ErrInvalidFilePath
	}

	fullPath := filepath.Join(baseAbs, path)
	fullPath = filepath.Clean(fullPath)

	fullPathAbs, err := filepath.Abs(fullPath)
	if err != nil {
		logger.Errorf("invalid path for %s: %v", fullPath, err)
		return "", ErrInvalidFilePath
	}

	if !strings.HasPrefix(fullPathAbs, baseAbs+string(os.PathSeparator)) &&
		fullPathAbs != baseAbs {
		logger.Errorf("path traversal detected: %s", fullPath)
		return "", ErrAccessDenied
	}

	return fullPathAbs, nil
}

func (s *Service) GetRelativePath(fullPath string) (string, error) {
	baseAbs, err := filepath.Abs(s.cfg.OutputDir)
	if err != nil {
		return "", err
	}

	// Convert fullPath to absolute path first to handle relative paths
	fullPathAbs, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(baseAbs, fullPathAbs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		rel = ""
	}
	return rel, nil
}
