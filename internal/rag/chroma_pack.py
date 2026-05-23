#!/usr/bin/env python3
"""chroma_pack.py — load chunks.jsonl into a ChromaDB PersistentClient.

Run via: python3 chroma_pack.py --chunks <chunks.jsonl> --out <chroma_dir> --collection <name>

Requires the chromadb Python package on the host. The Go-side `rag pack`
subcommand stages this script to a temp file at run time so the bundled
helper version stays pinned to the binary version.

This script does NOT re-embed: it expects each row in chunks.jsonl to
have an "embedding" array. Chroma's add() accepts pre-computed
embeddings; the collection is created with embedding_function=None so
Chroma won't try to wrap the model itself.
"""

import argparse
import json
import sys


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--chunks", required=True, help="Path to chunks.jsonl")
    parser.add_argument("--out", required=True, help="Output Chroma persistent directory")
    parser.add_argument("--collection", default="module", help="Chroma collection name")
    args = parser.parse_args()

    try:
        import chromadb
    except ImportError as e:
        print(f"chromadb not installed (pip install chromadb): {e}", file=sys.stderr)
        return 2

    client = chromadb.PersistentClient(path=args.out)
    # embedding_function=None: we provide vectors directly. Without
    # this, chromadb defaults to all-MiniLM-L6-v2 and tries to embed
    # at query time, which would conflict with our pre-computed
    # bge-large vectors.
    try:
        col = client.get_collection(name=args.collection)
    except Exception:
        col = client.create_collection(name=args.collection, embedding_function=None)

    ids, docs, embeddings, metadatas = [], [], [], []
    batch_size = 500
    flushed = 0

    def flush():
        nonlocal flushed
        if not ids:
            return
        col.upsert(
            ids=ids,
            documents=docs,
            embeddings=embeddings,
            metadatas=metadatas,
        )
        flushed += len(ids)
        print(f"upserted batch of {len(ids)} (total {flushed})", file=sys.stderr)
        ids.clear(); docs.clear(); embeddings.clear(); metadatas.clear()

    with open(args.chunks) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            row = json.loads(line)
            if not row.get("embedding"):
                # Skip un-embedded rows rather than fail — lets a
                # partial export still produce a usable Chroma DB.
                continue
            ids.append(row["id"])
            docs.append(row["text"])
            embeddings.append(row["embedding"])
            meta = {
                "module": row.get("module", ""),
                "lesson_index": row.get("lesson_index", -1),
                "lesson_title": row.get("lesson_title", ""),
                "type": row.get("type", ""),
            }
            if row.get("term"):
                meta["term"] = row["term"]
            metadatas.append(meta)
            if len(ids) >= batch_size:
                flush()
        flush()

    total = col.count()
    print(f"chroma collection '{args.collection}' has {total} entries", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
