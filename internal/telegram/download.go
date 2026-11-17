package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/tg"
)

const downloadPart = 512 * 1024

func (o *operation) download(ctx context.Context, r Request) (result string, err error) {
	e, err := o.resolve(ctx, r.Peer)
	if err != nil {
		return "", err
	}
	raw, err := o.message(ctx, e, r.MessageID)
	if err != nil {
		return "", err
	}
	m, ok := raw.(*tg.Message)
	if !ok {
		return "", userError("message carries no downloadable media")
	}
	location, dc, size, err := mediaLocation(m)
	if err != nil {
		return "", err
	}
	if size > MaxDownloadBytes {
		return "", userError("media exceeds the 100 MiB download limit")
	}
	pool, err := o.client.DC(ctx, dc, 1)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, pool.Close()) }()
	api := tg.NewClient(pool)
	return saveMedia(ctx, r.DestDir, r.MessageID, size, func(ctx context.Context, offset int64) ([]byte, error) {
		// Raw bounded reads intentionally avoid downloader's hidden FloodWait sleeps.
		response, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{Location: location, Offset: offset, Limit: downloadPart})
		if err != nil {
			return nil, err
		}
		if data, ok := response.(*tg.UploadFile); ok {
			return data.Bytes, nil
		}
		return nil, userError("Telegram returned an unsupported CDN redirect instead of a file")
	})
}

func mediaLocation(m *tg.Message) (tg.InputFileLocationClass, int, int64, error) {
	switch media := m.Media.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := media.Document.(*tg.Document); ok {
			return doc.AsInputDocumentFileLocation(""), doc.DCID, doc.Size, nil
		}
	case *tg.MessageMediaPhoto:
		if photo, ok := media.Photo.(*tg.Photo); ok {
			thumb := ""
			size := int64(0)
			area := 0
			for _, raw := range photo.Sizes {
				var kind string
				var w, h int
				var bytes int64
				switch s := raw.(type) {
				case *tg.PhotoSize:
					kind, w, h, bytes = s.Type, s.W, s.H, int64(s.Size)
				case *tg.PhotoSizeProgressive:
					kind, w, h = s.Type, s.W, s.H
					for _, v := range s.Sizes {
						bytes = max(bytes, int64(v))
					}
				case *tg.PhotoCachedSize:
					kind, w, h, bytes = s.Type, s.W, s.H, int64(len(s.Bytes))
				}
				if w*h > area {
					area = w * h
					thumb = kind
					size = bytes
				}
			}
			if thumb != "" {
				return photo.AsInputPhotoFileLocation(thumb), photo.DCID, size, nil
			}
		}
	}
	return nil, 0, 0, userError("message has no supported photo/document attachment (previews and external URLs are not downloaded)")
}

// saveMedia uses a confined directory handle and exclusive random names, never
// untrusted remote filenames. A failed/oversized/cancelled download is removed.
func saveMedia(ctx context.Context, dir string, id int, size int64, fetch func(context.Context, int64) ([]byte, error)) (result string, err error) {
	if size < 0 || size > MaxDownloadBytes {
		return "", userError("invalid media size or media exceeds 100 MiB")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", userError("cannot resolve home directory")
		}
		if dir == "~" {
			dir = home
		} else {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", userError("invalid destination directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", userError("cannot create destination directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", userError("cannot open destination directory")
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", userError("cannot generate local filename")
	}
	name := fmt.Sprintf("telegram-%d-%s.bin", id, hex.EncodeToString(random[:]))
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", userError("cannot create download file")
	}
	complete := false
	defer func() {
		if e := f.Close(); e != nil {
			err = errors.Join(err, userError("cannot close download file"))
			complete = false
		}
		if !complete {
			if e := root.Remove(name); e != nil {
				err = errors.Join(err, userError("cannot remove partial download; inspect destination directory"))
			}
		}
	}()
	written := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		data, err := fetch(ctx, written)
		if err != nil {
			return "", err
		}
		if len(data) > downloadPart || written+int64(len(data)) > MaxDownloadBytes {
			return "", userError("download exceeded the allowed chunk/100 MiB size")
		}
		if size > 0 && written+int64(len(data)) > size {
			return "", userError("download exceeded its announced size")
		}
		n, err := f.Write(data)
		if err != nil || n != len(data) {
			return "", userError("cannot write media to destination (disk full or access denied)")
		}
		written += int64(n)
		if size > 0 && written == size {
			break
		}
		if len(data) < downloadPart {
			if size > 0 && written != size {
				return "", userError("download ended before the announced size")
			}
			break
		}
	}
	if written == 0 {
		return "", userError("Telegram returned an empty file")
	}
	if err := f.Sync(); err != nil {
		return "", userError("cannot flush download file")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	complete = true
	return fmt.Sprintf("%s (%d bytes)", filepath.Join(dir, name), written), nil
}
