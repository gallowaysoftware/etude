#!/usr/bin/env python3
"""anki_pack.py — convert flashcards.tsv into a .apkg Anki deck.

Run via: python3 anki_pack.py --flashcards <tsv> --out <apkg> --deck-name <name>

The TSV has the header "Front\tBack\tLesson\tTags" — produced by
internal/rag.WriteFlashcardsTSV. Two card models: 'Basic' (the
default Anki shape: Front + Back) and our deck uses it for both MC
and SA cards. Tags carry the lesson + question_type so Anki users
can filter / sub-deck.

Requires the genanki package (pip install genanki). The Go-side
PackAnki invocation expects this script to land in a venv at
~/.local/state/textbook-to-audiobook/rag-venv with genanki
installed.
"""

import argparse
import csv
import hashlib
import sys


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--flashcards", required=True)
    p.add_argument("--out", required=True)
    p.add_argument("--deck-name", default="CIBD Study")
    args = p.parse_args()

    try:
        import genanki
    except ImportError as e:
        print(f"genanki not installed (pip install genanki): {e}", file=sys.stderr)
        return 2

    # Stable deterministic IDs derived from the deck name — same
    # deck name yields the same id across reruns so re-imports
    # update existing cards instead of duplicating.
    def stable_id(seed: str) -> int:
        h = hashlib.sha256(seed.encode()).hexdigest()
        # Anki IDs are 31-bit signed ints in practice.
        return int(h[:8], 16) & 0x7FFFFFFF

    deck_id = stable_id("deck:" + args.deck_name)
    model_id = stable_id("model:cibd-study-basic")

    model = genanki.Model(
        model_id,
        "CIBD Study Basic",
        fields=[{"name": "Front"}, {"name": "Back"}, {"name": "Lesson"}],
        templates=[
            {
                "name": "Card 1",
                "qfmt": "{{Front}}",
                "afmt": "{{FrontSide}}<hr id='answer'>{{Back}}<br><br><small><i>{{Lesson}}</i></small>",
            }
        ],
        css="""
.card {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  font-size: 18px;
  text-align: left;
  color: #2a2a2a;
  background-color: #fafaf7;
  padding: 16px;
}
.card.night_mode {
  color: #f0eee6;
  background-color: #1c1c1a;
}
hr#answer { border: 0; border-top: 1px solid #c87143; margin: 16px 0; }
small { color: #888; }
""",
    )
    deck = genanki.Deck(deck_id, args.deck_name)

    seen_card_ids = set()
    with open(args.flashcards, newline="") as f:
        reader = csv.DictReader(f, delimiter="\t")
        for i, row in enumerate(reader):
            front = (row.get("Front") or "").strip()
            back = (row.get("Back") or "").strip()
            lesson = (row.get("Lesson") or "").strip()
            tags_str = (row.get("Tags") or "").strip()
            if not front or not back:
                continue
            tags = [t for t in tags_str.split() if t]
            # Deterministic card id so re-imports update existing.
            card_seed = front + "::" + back
            card_id = stable_id("note:" + card_seed)
            if card_id in seen_card_ids:
                # Hash collision (extremely unlikely with sha256 → int31);
                # nudge by appending the row index.
                card_id = stable_id("note:" + card_seed + ":" + str(i))
            seen_card_ids.add(card_id)
            note = genanki.Note(
                model=model,
                fields=[front, back, lesson],
                tags=tags,
                guid=str(card_id),
            )
            deck.add_note(note)

    genanki.Package(deck).write_to_file(args.out)
    print(
        f"wrote {len(seen_card_ids)} cards to {args.out} (deck '{args.deck_name}')",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
