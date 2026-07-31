# Budgeting Video Memory

## The full equation

"Does this model fit on my card?" is really four questions added
together:

total memory = weights + KV cache + compute buffers + what was already there

The weights come from the file size. The KV cache comes from the
formula in the previous lesson, evaluated at the context length you
actually intend to run — not the maximum the model advertises. Compute
buffers hold the intermediate activations of the tokens in flight; they
scale with batch size and are usually a few hundred megabytes for
single-user chat. The last term is everything the machine was doing
before you loaded a model: the operating system, the browser, and on a
display-connected GPU the desktop compositor, which can hold a gigabyte
or more on its own.

## A worked budget

Suppose a 24 GB card in a desktop that also drives a monitor:

- Desktop and OS reserve: ~1.5 GB
- 13B model at 4-bit: ~7.5 GB of weights
- KV cache at 8,192 tokens, FP16: ~2 GB (worked from the layer and
  head counts, as in the previous lesson)
- Compute buffers at default batch: ~0.5 GB

The sum is about 11.5 GB against 24 GB, and the model fits with room
to extend the context several times over. Run the same arithmetic
against an 8 GB card and it fails before the KV cache is even counted:
7.5 GB of weights plus 1.5 GB of desktop is already over budget. The
arithmetic is the same in both cases; only the answer differs.

## Headroom is not waste

A budget that lands within a few percent of the card's capacity is a
budget that will fail in practice. Memory allocators fragment, drivers
reserve regions they do not report, and a context that grows past the
planned length has nowhere to go. The working rule is to leave 10–15%
of the card unallocated on paper. If the budget only balances at 99%,
the correct moves are a smaller quant, a shorter context, or partial
offload — not optimism.

## Unified memory changes the ledger, not the math

On Apple silicon the GPU and CPU share one pool, so there is no VRAM
wall — but the same four terms now compete with the entire operating
system in one budget, and the OS takes its share first. A 16 GB
machine behaves like a card with considerably less than 16 GB, and the
penalty for over-budgeting is not a failed load but swap, which the
next lesson covers. Whether the pool is called VRAM or unified memory,
the discipline is identical: add the four terms, leave headroom, and
treat the file size as the floor rather than the total.
