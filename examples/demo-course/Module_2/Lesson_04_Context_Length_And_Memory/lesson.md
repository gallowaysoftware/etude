# Context Length and Memory

## Context is rented, not owned

A model's context window is often advertised like a feature of the
file — "128k context" — but at runtime every token of context is memory
you are actively paying for. The KV cache formula from module 1 makes
the terms explicit: cache size scales linearly with the number of
tokens in the context, at a rate set by the model's layers and KV
heads. A model whose weights idle at 4 GB can demand another 4 GB of
cache at 32,768 tokens with an FP16 cache. The advertised window is a
ceiling the architecture can express, not an allotment your hardware
has budgeted.

## The ceiling itself

Two limits decide how far the context can actually stretch. The first
is training: a model trained with 8,192-token sequences degrades beyond
them because its positional encoding has never seen those positions,
and techniques like RoPE scaling can stretch the window at some cost to
quality — but they stretch what the model can read, not what your
memory can hold. The second limit is the cache itself. On home
hardware, memory usually binds first: the model would happily attend
over 100,000 tokens if you could store 100,000 tokens of keys and
values.

## Quantizing the cache

The same trick that shrinks weights applies to the KV cache, and most
runtimes offer it as a separate flag. Storing keys and values at 8 bits
instead of 16 halves the cache; 4-bit storage quarters it, at the cost
of a small, measurable quality loss on long contexts — attention over
tens of thousands of tokens is exactly where key precision matters
most, so cache quantization degrades gracefully at 8 bits and more
visibly at 4. The decision mirrors weight quantization: a nearly free
halving first, then a cheaper-but-real quartering when the budget
demands it.

## What does not help

Flash attention and similar fused kernels shrink the temporary buffers
attention needs while computing, sometimes dramatically, but they do
not shrink the stored keys and values — the cache is the cache.
Batching more concurrent conversations multiplies the cache rather than
amortizing it, since each conversation keeps its own. When a
long-document session runs out of memory on hardware that handled the
same model easily at short context, the fix list is short and
unpleasant: shorten the context, quantize the cache, or free memory
elsewhere in the budget. There is no kernel flag that makes 100,000
tokens weigh less than the formula says.
