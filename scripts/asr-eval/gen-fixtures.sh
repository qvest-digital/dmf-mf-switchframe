#!/bin/bash
# Generates synthetic speech WAV fixtures + reference transcripts for ASR
# evaluation. Reads scripts/asr-eval/fixtures.txt (one row per fixture in
# `name|rate|voice|text` format), invokes macOS `say` to render each at the
# requested rate as 16kHz mono int16 WAV, and writes a stripped-down
# reference transcript next to each WAV.
#
# Reference transcripts have `[[slnc N]]` directives stripped (those affect
# audio timing only, not what whisper should transcribe).
#
# Re-running is idempotent: existing WAVs are replaced.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
FIXTURES_FILE="$SCRIPT_DIR/fixtures.txt"
OUT_DIR="$REPO_ROOT/test/asr-fixtures"

if [[ "$(uname)" != "Darwin" ]]; then
    echo "ERROR: this script uses macOS \`say\`. Linux equivalents (espeak/festival) " >&2
    echo "would need different invocations -- not implemented yet."                    >&2
    exit 1
fi

mkdir -p "$OUT_DIR"

count=0
while IFS= read -r line; do
    # Skip blanks and comments.
    [[ -z "$line" || "$line" == \#* ]] && continue

    IFS='|' read -r name rate voice text <<< "$line"
    if [[ -z "$name" || -z "$rate" || -z "$text" ]]; then
        echo "WARN: malformed line: $line" >&2
        continue
    fi

    wav="$OUT_DIR/${name}.wav"
    ref="$OUT_DIR/${name}.ref.txt"

    echo "  $name ($rate wpm, voice=$voice)"

    # Strip [[slnc N]] from the reference (audio-only directive).
    cleaned=$(echo "$text" | sed -E 's/\[\[slnc[[:space:]]+[0-9]+\]\]//g' | tr -s ' ')
    echo "$cleaned" > "$ref"

    # Render audio. -r sets words/min. --data-format=LEI16@16000 emits a
    # 16kHz mono signed-16-bit WAV (whisper's native input).
    say -r "$rate" -v "$voice" --data-format=LEI16@16000 -o "$wav" "$text"

    count=$((count + 1))
done < "$FIXTURES_FILE"

echo "Generated $count fixtures in $OUT_DIR"
