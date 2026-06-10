package agentapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var embeddedWeb embed.FS

// WebFiles is the FS rooted at the web/ directory.
var WebFiles = func() fs.FS {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	return sub
}()

var _ http.FileSystem = http.FS(WebFiles)
