# What Quantization Costs

## Measuring the damage

The standard yardstick is perplexity: how surprised the model is by
held-out text, lower being better. Quantize a model and its perplexity
rises, and the shape of that rise is the single most important fact in
this module. From 16 bits down to 8 the increase is tiny — often within
measurement noise — because 256 levels per block is generous resolution
for weight distributions. Through 6 and 5 bits the climb is gentle. At
4 bits it becomes noticeable but modest. Below 4 bits the curve bends
sharply upward: each additional bit removed doubles the spacing between
representable values, and 3-bit and especially 2-bit files show damage
that perplexity barely understates. Quantization is nearly free at the
top of the ladder and increasingly expensive at the bottom.

## What breaks first

The degradation is not uniform across abilities. The capacities that
rely on the model's margins — the cases where the right continuation
only slightly outranks the plausible wrong ones — erode earliest:

- Long chains of reasoning, where a small drift at each step compounds
  into a wrong conclusion.
- Arithmetic and exact symbol manipulation, which have no redundancy to
  absorb rounding error.
- Recall of rare facts, whose weights carry little training signal and
  sit closest to the noise floor.
- Instruction following over long prompts, where sustained precision
  matters more than local fluency.

Fluency and common knowledge survive longest, which is why a heavily
quantized model can chat convincingly right up until you ask it to do
something hard.

## The other axis: model size

The cost of a bit is not fixed — it shrinks as the model grows. A 70B
model quantized to 3 bits has suffered the same per-weight rounding as
a 7B at 3 bits, but its enormous redundancy absorbs the error, and in
practice it outperforms the 7B model at full precision on most tasks.
This is the trade that makes the bottom of the ladder worth visiting at
all: within a fixed memory budget, "larger model, fewer bits" usually
beats "smaller model, more bits" until the bit width falls below about
3, where even large models lose coherence. The budget question is
therefore never "which quant is best" but "which model at which quant
fits, and which side of the 3-bit floor does that put me on."
