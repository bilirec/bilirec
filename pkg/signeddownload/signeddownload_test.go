package signeddownload_test

import (
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/signeddownload"
	"github.com/stretchr/testify/assert"
)

func TestGenerateAndParseDownloadToken(t *testing.T) {
	secret := []byte("my_secret_key")
	client := signeddownload.NewClient(secret)

	filePath := "/path/to/myfile.mp4"
	expiration := time.Now().Add(1 * time.Hour).Unix()

	token, err := client.GenerateDownloadToken(filePath, expiration)
	assert.NoError(t, err, "Generating download token should not produce an error")

	claims, err := client.ParseDownloadToken(token)
	assert.NoError(t, err, "Parsing download token should not produce an error")
	assert.Equal(t, filePath, claims.FilePath, "File path in claims should match the original")
	assert.Equal(t, expiration, claims.Exp, "Expiration in claims should match the original")
}
