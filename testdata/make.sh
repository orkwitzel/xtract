#!/usr/bin/env bash
# Builds testdata/sample.zip, an archive for trying xtract out by hand.
#
# It is deliberately awkward: five levels deep, six formats mixed together, a
# zip wearing a .txt extension, an archive with no extension at all, a lone
# .gz that should not get a directory, and a .docx that should be left alone
# unless --all is passed.
#
#   ./testdata/make.sh
#   go build -o xtract . && ./xtract testdata/sample.zip
#
# Needs zip, tar, gzip, bzip2, xz, zstd and 7z on PATH. For a random tree
# instead of this fixed one, see random.sh.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
out=$here/sample.zip
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

need() { command -v "$1" >/dev/null || { echo "make.sh: need $1 on PATH" >&2; exit 1; }; }
for t in zip tar gzip bzip2 xz zstd 7z; do need "$t"; done

# Build inside out: each layer is packed, then packed into the next one.
cd "$work"

# --- level 5: the bottom of the tree -----------------------------------------
mkdir -p b && printf 'You made it all the way down.\n' > b/finally.txt
(cd b && zip -qr ../bottom.zip .)

# --- level 4: a tar.zst holding that zip --------------------------------------
mkdir -p d
printf 'Treasure at depth four.\n' > d/treasure.txt
mv bottom.zip d/
tar -C d --zstd -cf deep.tar.zst .

# --- level 3: a 7z holding the tar.zst ----------------------------------------
7z a -bso0 -bsp0 stage2.7z deep.tar.zst >/dev/null
rm deep.tar.zst

# --- level 2: a tar.gz holding the 7z, plus some odd shapes -------------------
mkdir -p s2
printf '# Stage one\n\nHalfway down.\n' > s2/notes.md
mv stage2.7z s2/

# A zip with a misleading extension. Detection is by content, so it is still
# found and extracted.
mkdir -p m && printf 'Named .txt, actually a zip.\n' > m/surprise.txt
(cd m && zip -qr ../weird.txt .)
mv weird.txt s2/

# An archive with no extension at all.
mkdir -p n && printf 'No extension, still an archive.\n' > n/hidden.txt
# zip insists on adding .zip when the name has none, so take it off again.
(cd n && zip -qr ../noext_archive.zip .)
mv noext_archive.zip s2/noext_archive

tar -C s2 -czf stage1.tar.gz .

# --- level 1: the root, plus files that exercise the policies -----------------
mkdir -p root/docs root/logs root/mixed
mv stage1.tar.gz root/

printf 'xtract sample archive.\n\nEverything under here came out of one file.\n' > root/readme.txt

# A lone compressed file: becomes access.log in place, no directory of its own.
for i in $(seq 1 200); do printf '127.0.0.1 - - [30/Aug/2026] "GET /page/%d" 200\n' "$i"; done > root/logs/access.log
gzip -9 root/logs/access.log

# A tar.bz2, for one more codec in the mix.
mkdir -p p && printf 'JPEG-ish\n' > p/one.jpg && printf 'PNG-ish\n' > p/two.png
tar -C p -cjf root/mixed/photos.tar.bz2 .

# A .docx: a zip by construction, a document to everyone else. Skipped unless
# --all is passed.
mkdir -p docx/word
cat > 'docx/[Content_Types].xml' <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
</Types>
XML
cat > docx/word/document.xml <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>Leave me alone unless --all.</w:t></w:r></w:p></w:body>
</w:document>
XML
(cd docx && zip -qr ../report.docx .)
mv report.docx root/docs/

# An xz'd tarball at the top level too, so the root has more than one branch.
mkdir -p cfg && printf 'level = 11\n' > cfg/settings.toml
tar -C cfg -cJf root/config.tar.xz .

# zip and 7z update an existing archive rather than replacing it.
rm -f "$out"
(cd root && zip -qr "$out" .)

echo "wrote $out ($(du -h "$out" | cut -f1))"
