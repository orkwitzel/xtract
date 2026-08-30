package extractor

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mholt/archives"
)

// headSize is how many leading bytes we keep from each extracted file to
// decide whether it is itself an archive. Tar needs a full 512-byte header
// block to match; every other format needs far less.
const headSize = 1024

// format is what we learned about a file. It never leaves this package.
type format struct {
	name      string // "zip", "tar.gz", "rar", ... empty when unknown
	isArchive bool
	container bool // a zip that is really a document or package (.docx, .jar, ...)
}

// containerExts are formats that happen to be zips but that nobody wants
// exploded into XML by default. --all turns them back into ordinary archives.
var containerExts = map[string]bool{
	".docx": true, ".xlsx": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true,
	".jar": true, ".war": true, ".ear": true,
	".apk": true, ".aab": true, ".ipa": true,
	".epub": true, ".whl": true, ".egg": true,
	".crx": true, ".xpi": true,
	".nupkg": true, ".vsix": true, ".msix": true, ".appx": true,
}

// detect classifies a file from its leading bytes. The name is used only to
// spot container formats; identification itself is done purely from the
// stream, because nested files routinely have no extension at all, and a
// misleading one is worse than none.
func detect(ctx context.Context, name string, head []byte) format {
	if len(head) == 0 || isContinuationVolume(name) {
		return format{}
	}
	f, _, err := archives.Identify(ctx, "", bytes.NewReader(head))
	if err != nil {
		return format{} // NoMatch, or not enough bytes to tell
	}
	ext := strings.ToLower(f.Extension())
	if weakMagic[ext] && !strings.HasSuffix(strings.ToLower(name), ext) {
		return format{}
	}
	return format{
		name:      strings.TrimPrefix(f.Extension(), "."),
		isArchive: true,
		container: containerExts[strings.ToLower(filepath.Ext(name))],
	}
}

// weakMagic lists formats whose header is too thin to believe on its own.
//
// A zlib stream is identified by two bytes drawn from a table of 32, so one
// random buffer in 1880 matches; most of those are then thrown out because the
// stream will not decompress, but roughly one binary file in 66,000 still gets
// through and is "extracted" into garbage, failing an otherwise fine run. A
// bare zlib file on disk is rare and carries the extension when it exists, so
// these formats are believed only when the name agrees.
var weakMagic = map[string]bool{".zz": true}

// Continuation volumes of a split rar carry a full rar header, so they look
// like archives in their own right. Extracting one on its own fails, and
// worse, it could be deleted while the first volume still needs it. Only the
// first volume is ever treated as an archive; the decoder pulls in the rest.
//
// Split 7z and zip sets need no such rule: only their first part carries the
// signature, so the others simply never match.
var (
	rarPartRe = regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)
	rarOldRe  = regexp.MustCompile(`(?i)\.r\d{2,}$`)
)

func isContinuationVolume(name string) bool {
	if m := rarPartRe.FindStringSubmatch(name); m != nil {
		n, err := strconv.Atoi(m[1])
		return err != nil || n != 1
	}
	return rarOldRe.MatchString(name)
}

// headWriter captures the first headSize bytes written through it while
// passing everything along, so detection costs no extra read of the file.
type headWriter struct {
	head []byte
}

func (h *headWriter) Write(p []byte) (int, error) {
	if n := headSize - len(h.head); n > 0 {
		h.head = append(h.head, p[:min(n, len(p))]...)
	}
	return len(p), nil
}
