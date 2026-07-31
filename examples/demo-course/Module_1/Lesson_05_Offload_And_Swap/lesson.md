# Offload and Swap

## When the model is bigger than the card

A model whose weights exceed VRAM is not automatically unusable; it is
usable at a price, and this lesson is about reading the price tag.
There are three graduated strategies — partial GPU offload, memory
mapping, and swap — and they differ by orders of magnitude in what they
cost you per token.

## Partial offload

Runtimes like llama.cpp can place some transformer layers on the GPU
and the rest in system RAM, exposed as the "number of GPU layers"
setting. Every generated token then flows through CPU layers and GPU
layers in sequence. Because generation is memory-bandwidth bound — each
token requires reading essentially every weight once — the token rate
of a split model is bounded by the slowest leg. CPU memory bandwidth is
typically a fifth to a tenth of a midrange GPU's, so a model with half
its layers on CPU runs far closer to CPU speed than to GPU speed.
Offload is how you make an oversized model run at all; it is not a way
to make it run fast.

## Memory mapping

Because GGUF files are built for `mmap`, a loader can avoid reading the
weights into RAM at all: the operating system pages tensor data in from
disk as the computation touches it. On a fast NVMe drive this makes
startup nearly instant and lets a model larger than RAM load without
swap. The catch appears on reuse: weights the OS had to evict are read
from disk again on the next pass. For a single short generation that is
fine; for a long chat, where every token sweeps the full weight set,
evicted pages turn into steady disk traffic and the token rate sags
toward the drive's read speed.

## Swap, the last resort

Swap is what happens when the working set — the weights, KV cache, and
buffers the run is actively touching — exceeds physical RAM and the OS
pushes pages to disk to make room. Inference touches almost all of its
memory on every token, so once the working set spills, every token
stalls on disk I/O. Throughput collapses from tokens per second to
seconds per token, and the drive wears under continuous traffic. Swap
has exactly one legitimate use here: proving a model loads and produces
coherent text before you commit to a smaller quant or more hardware.

## Choosing between them

The strategies stack in a clear order. Fit the working set in VRAM if
the budget allows it. If not, offload layers until it fits and accept
the slower rate. If RAM alone cannot hold the working set, mmap keeps
the model usable for bursty, low-duty work. Swap is a diagnostic, not a
configuration. Every step down this ladder trades the same currency —
memory bandwidth — and the token rate is the exchange rate made
visible.
