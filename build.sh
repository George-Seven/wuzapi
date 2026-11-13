#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
WHATSMEOW_DIR="$SCRIPT_DIR/whatsmeow"

if [[ ! -d "$WHATSMEOW_DIR/.git" && ! -f "$WHATSMEOW_DIR/.git" ]]; then
	echo "whatsmeow submodule is missing; initialize it with: git submodule update --init whatsmeow" >&2
	exit 1
fi

shopt -s nullglob
PATCHES=("$SCRIPT_DIR"/patches/[0-9]*.patch)
for patch_file in "${PATCHES[@]}"; do
	if git -C "$WHATSMEOW_DIR" apply --check "$patch_file" >/dev/null 2>&1; then
		git -C "$WHATSMEOW_DIR" apply "$patch_file"
		printf 'Applied %s\n' "${patch_file##*/}"
	elif git -C "$WHATSMEOW_DIR" apply --check --reverse "$patch_file" >/dev/null 2>&1; then
		printf 'Already applied: %s\n' "${patch_file##*/}"
	else
		echo "Cannot apply $patch_file to the whatsmeow submodule. Check the patch base revision and any local changes." >&2
		exit 1
	fi
done

if [[ -n "${TERMUX_VERSION:-}" && -n "${PREFIX:-}" ]]; then
	exec "$SCRIPT_DIR/build_termux.sh" "$@"
fi

OUTPUT="$SCRIPT_DIR/wuzapi"
OUTPUT_SET=0
while [[ $# -gt 0 ]]; do
	case "$1" in
		-o|--output)
			if [[ $# -lt 2 ]]; then
				echo "Missing output path after $1" >&2
				exit 2
			fi
			OUTPUT="$2"
			OUTPUT_SET=1
			shift 2
			;;
		--run-test)
			echo "--run-test is only available in Termux (it checks bundled Termux resources)." >&2
			exit 2
			;;
		-*)
			echo "Unknown option: $1" >&2
			exit 2
			;;
		*)
			if [[ "$OUTPUT_SET" -eq 1 ]]; then
				echo "Output path specified more than once" >&2
				exit 2
			fi
			OUTPUT="$1"
			OUTPUT_SET=1
			shift
			;;
	esac
done

cd "$SCRIPT_DIR"
go build -o "$OUTPUT" .
echo "Built $OUTPUT with whatsmeow patches applied."
