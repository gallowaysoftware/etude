# Quantization SAQ

Self-check questions for the module on quantization and what it costs.
Answer each from memory first, then compare against the suggested
responses.

## Self-Assessment Questions

1. \* What does quantization change about a model, and what does it
   leave unchanged?
2. \* Decode the filename code Q4_K_M: what does each of the three
   parts tell you?
3. \*\* Why is 8-bit quantization nearly free while 3-bit quantization
   visibly damages a model?
4. \*\* List four capabilities that degrade first at low bit widths,
   and explain what they have in common.
5. \*\* Within a fixed memory budget, why does a 13B model at 4 bits
   usually beat a 7B model at 8 bits, and where does that trade stop
   working?
6. \*\* Why does quantizing the KV cache matter more at long context
   than at short context, and what does cache quantization cost?
7. \*\*\* You have a 16 GB memory budget after the OS and headroom, and
   want the best chat quality you can run at 8k context. Outline the
   decision procedure you would follow to pick a model and quant.

## Suggested Response - Q1

Response:

Quantization changes only how the weights are stored: 16-bit floats are
re-expressed as low-bit codes with per-block scale factors, and the
loader expands the codes back to full precision as the matrix
multiplication consumes them.

It leaves unchanged:

- The architecture: layers, heads, and context length are identical to
  the original model.
- The arithmetic: computation runs on full-precision values after
  dequantization, not on the low-bit codes.
- The KV cache and activations, which stay at full precision unless a
  separate option quantizes the cache.

## Suggested Response - Q2

Response:

- Q4: the nominal bit width — four bits per weight before overhead,
  roughly half a byte per parameter.
- K: the k-quants, llama.cpp's block-encoding family, which groups
  blocks into superblocks sharing a higher-precision scale to cut
  scale overhead.
- M: the size mix. Sensitive tensors such as the embedding table and
  output projection are stored at a higher width, and M/S/L sets how
  generous that exception budget is — so the file averages about 4.8
  bits per weight rather than exactly 4.

## Suggested Response - Q3

Response:

Each bit removed doubles the spacing between representable values
within a block. At 8 bits there are 256 levels per block, generous
resolution for typical weight distributions, so the rounding error is
tiny and perplexity barely moves.

At 3 bits there are only 8 levels. The rounding error per weight is
much larger, and the errors accumulate across the hundreds of matrix
multiplications each token passes through. The damage concentrates
where the model was already uncertain, so perplexity climbs sharply and
behavior degrades visibly below about 4 bits.

## Suggested Response - Q4

Response:

- Long chains of reasoning, where small per-step drift compounds into
  wrong conclusions.
- Arithmetic and exact symbol manipulation, which have no redundancy
  to absorb rounding error.
- Recall of rare facts, whose weights carry little training signal and
  sit closest to the noise floor.
- Instruction following over long prompts, where sustained precision
  matters more than local fluency.

What they have in common: each relies on the model's margins — cases
where the right continuation only slightly outranks a plausible wrong
one — so each is eroded first when quantization noise narrows those
margins. Fluency and common knowledge have wide margins and survive
longest.

## Suggested Response - Q5

Response:

The cost of one bit of precision shrinks as a model grows: a 13B model
has enough redundancy to absorb 4-bit rounding error and still
outperform a 7B model on most tasks, while the 8-bit 7B cannot recover
the capability gap that six billion extra parameters provide. So within
a fixed budget, more parameters at fewer bits usually wins.

The trade stops working at the bottom of the ladder — below roughly 3
bits, the per-weight error grows so large that even a large model's
redundancy cannot absorb it, and coherence itself degrades. Past that
floor, the smaller, more precise model is the better choice again.

## Suggested Response - Q6

Response:

The KV cache grows linearly with context length while the weights stay
fixed, so at long context the cache becomes a large — sometimes
dominant — share of the memory budget. Halving or quartering the
cache's element size is then worth gigabytes, enough to decide whether
a long-context session fits at all; at short context the cache is small
and quantizing it saves little.

The cost is attention quality over long ranges: keys and values stored
at 8 bits degrade quality only slightly, while 4-bit cache storage
shows a more visible drop precisely on the long-context tasks that
motivated it, because attention over tens of thousands of tokens is
where key precision matters most.

## Suggested Response - Q7

Response:

- Fix the budget: 16 GB minus the KV cache at 8k context for the
  candidate model (computed from its layers, KV heads, head width, and
  element size) — the remainder is the weight allowance.
- Find the largest model whose weights fit the allowance at the
  highest bit width available, preferring K-quants (Q4_K_M before
  Q4_K_S) and imatrix/I-quant files below 4 bits.
- Check that the total — weights plus cache plus compute buffers —
  still leaves 10–15% of the original budget free; if not, step down a
  quant size or a model size.
- Sanity-check the finalist on your own prompts, since general
  perplexity rankings do not include your workload.
