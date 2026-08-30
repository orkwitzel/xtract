#!/usr/bin/env bash
# Builds a random nested archive for throwing at xtract.
#
# Each layer gets some random files and, unless it is the last one, a few child
# archives built the same way. Every archive picks its format at random from
# whatever packers are installed, so a single tree mixes zip, tar.gz, 7z,
# tar.zst and the rest — which is the interesting case.
#
#   ./testdata/random.sh -d 5              # five layers deep
#   ./testdata/random.sh -d 4 -b 3 --weird # wider, with misleading names
#   ./testdata/random.sh --seed 1234       # same shape and names as before
#
set -euo pipefail

depth=3 breadth=2 files=3 size=64 out= seed= weird=0

usage() {
	cat <<'USAGE'
usage: random.sh [options]

  -d, --depth N     how many layers of nesting (default 3)
  -b, --breadth N   child archives per layer (default 2)
  -f, --files N     plain files per layer (default 3)
  -s, --size N      largest plain file, in KB (default 64)
  -o, --out PATH    where to write it (default random.<format> here).
                    A known extension picks the outermost format.
      --seed N      rebuild the same shape and names as an earlier run; the
                    seed is printed every time. File contents come from
                    /dev/urandom and are always fresh
      --weird       give some nested archives a misleading extension or none
                    at all, so detection has to go on content
  -h, --help

Total archives is (breadth^depth - 1) / (breadth - 1), so it grows quickly:
-d 6 -b 3 is 364 archives.
USAGE
}

die() { echo "random.sh: $*" >&2; exit 1; }

while (($#)); do
	case $1 in
	-d | --depth) depth=$2; shift 2 ;;
	-b | --breadth) breadth=$2; shift 2 ;;
	-f | --files) files=$2; shift 2 ;;
	-s | --size) size=$2; shift 2 ;;
	-o | --out) out=$2; shift 2 ;;
	--seed) seed=$2; shift 2 ;;
	--weird) weird=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option $1" ;;
	esac
done

for n in depth breadth files size; do
	[[ ${!n} =~ ^[0-9]+$ ]] || die "--$n takes a number, got '${!n}'"
done
((depth >= 1)) || die "--depth must be at least 1"
((size >= 1)) || die "--size must be at least 1"

# Only offer formats we can actually write.
formats=()
have() { command -v "$1" >/dev/null 2>&1; }
have zip && formats+=(zip)
have 7z && formats+=(7z)
if have tar; then
	formats+=(tar)
	have gzip && formats+=(tar.gz)
	have bzip2 && formats+=(tar.bz2)
	have xz && formats+=(tar.xz)
	have zstd && formats+=(tar.zst)
fi
((${#formats[@]})) || die "no packers found; install at least zip or tar"

# Everything that consumes $RANDOM sets REPLY instead of echoing. A $( )
# substitution forks, so the parent's sequence would not advance and two calls
# in a row would hand back the same answer.
pick() { REPLY=${*:$((RANDOM % $# + 1)):1}; }

words=(alpha bravo cinder delta ember frost glint harbor indigo jetty
	kelp lumen marrow nimbus onyx pallet quartz rivet saffron tundra
	umber vellum willow xenon yarrow zephyr)
exts=(txt log dat json csv md bin)
serial=0

# stem sets REPLY to a readable name that is unique across the whole run, so
# nothing collides no matter how the random names fall.
stem() {
	serial=$((serial + 1))
	REPLY="${words[RANDOM % ${#words[@]}]}$serial"
}

nfiles=0
narchives=0

# makefile writes one random file: sometimes raw bytes, more often base64 of
# them, so the tree holds a mix of compressible and incompressible data.
makefile() {
	local dir=$1 name bytes
	stem; name=$REPLY
	bytes=$(((RANDOM % (size * 1024)) + 1))
	head -c "$bytes" /dev/urandom >"$dir/$name.raw"
	if ((RANDOM % 3 == 0)); then
		mv "$dir/$name.raw" "$dir/$name.bin"
	else
		{
			echo "# $name"
			base64 "$dir/$name.raw"
		} >"$dir/$name.${exts[RANDOM % ${#exts[@]}]}"
		rm "$dir/$name.raw"
	fi
	nfiles=$((nfiles + 1))
}

# childname sets REPLY to the filename a nested archive should get. With
# --weird that is sometimes a lie, or missing entirely.
childname() {
	local fmt=$1 name
	stem; name=$REPLY
	if ((weird)) && ((RANDOM % 3 == 0)); then
		if ((RANDOM % 2 == 0)); then
			REPLY=$name
		else
			REPLY="$name.${exts[RANDOM % ${#exts[@]}]}"
		fi
	else
		REPLY="$name.$fmt"
	fi
}

pack() {
	local dir=$1 out=$2 fmt=$3
	case $fmt in
	zip) (cd "$dir" && zip -qry "$out" .) ;;
	7z) (cd "$dir" && 7z a -bso0 -bsp0 "$out" . >/dev/null) ;;
	tar) tar -C "$dir" -cf "$out" . ;;
	tar.gz) tar -C "$dir" -czf "$out" . ;;
	tar.bz2) tar -C "$dir" -cjf "$out" . ;;
	tar.xz) tar -C "$dir" -cJf "$out" . ;;
	tar.zst) tar -C "$dir" --zstd -cf "$out" . ;;
	*) die "cannot pack $fmt" ;;
	esac
	narchives=$((narchives + 1))
}

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

# build LAYERS OUT FMT fills a directory and packs it. It recurses before
# packing, so the tree is built from the inside out.
build() {
	local layers=$1 out=$2 fmt=$3
	local dir i cfmt cname first
	dir=$(mktemp -d "$scratch/layer.XXXXXX")

	for ((i = 0; i < files; i++)); do makefile "$dir"; done

	# A relative symlink now and then, since extracting one safely is a thing
	# xtract has to get right.
	if ((files > 0 && RANDOM % 3 == 0)); then
		first=$(ls "$dir" | head -1)
		stem
		ln -s "$first" "$dir/$REPLY.link"
	fi

	if ((layers > 1)); then
		for ((i = 0; i < breadth; i++)); do
			pick "${formats[@]}"; cfmt=$REPLY
			childname "$cfmt"; cname=$REPLY
			build $((layers - 1)) "$dir/$cname" "$cfmt"
		done
	fi

	pack "$dir" "$out" "$fmt"
	rm -rf "$dir"
}

# A known extension on --out picks the outermost format; anything else leaves
# it random. Two-part suffixes have to be tried first.
fmtof() {
	local name=${1,,} f
	for f in tar.gz tar.bz2 tar.xz tar.zst zip 7z tar; do
		[[ $name == *".$f" ]] && { REPLY=$f; return; }
	done
	REPLY=
}

seed=${seed:-$RANDOM}
[[ $seed =~ ^[0-9]+$ ]] || die "--seed takes a number, got '$seed'"
RANDOM=$seed

if [[ -n $out ]]; then
	fmtof "$out"
	rootfmt=$REPLY
	if [[ -n $rootfmt ]] && [[ " ${formats[*]} " != *" $rootfmt "* ]]; then
		die "no packer installed for $rootfmt"
	fi
fi
if [[ -z ${rootfmt:-} ]]; then
	pick "${formats[@]}"
	rootfmt=$REPLY
fi
out=${out:-random.$rootfmt}

mkdir -p "$(dirname "$out")"
out=$(cd "$(dirname "$out")" && printf '%s/%s' "$PWD" "$(basename "$out")")
# zip and 7z update an existing archive rather than replacing it, and an
# earlier run's file would be appended to instead of overwritten.
rm -f "$out"

build "$depth" "$out" "$rootfmt"

printf 'wrote %s\n' "$out"
printf '  %d layers · %d archives · %d files · %s · seed %d\n' \
	"$depth" "$narchives" "$nfiles" "$(du -h "$out" | cut -f1)" "$seed"
printf '  formats in play: %s\n' "${formats[*]}"
