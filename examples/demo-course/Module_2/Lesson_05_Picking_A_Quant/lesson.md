# Picking a Quant

## The decision procedure

Choosing a quantization is a budgeting exercise with a quality check at
the end, not a matter of taste. Worked in order:

1. Fix the memory budget. Total VRAM or unified memory, minus the OS
   and desktop, minus headroom of 10–15%, minus the KV cache at the
   context length you actually plan to run.
2. Find the largest model whose weights fit the remainder at the
   highest available bit width. Higher bit width and more parameters
   both cost memory; spend the budget on whichever mix the previous
   lesson's tradeoff favors — usually the larger model down to about
   4 bits.
3. Within that model, take the K-quant at the size mix that fits —
   Q4_K_M before Q4_K_S, Q5_K_M before Q4_K_M when the budget allows
   the jump.
4. Below 4 bits, prefer imatrix or I-quant files, which spend their
   scarce precision where calibration says it matters.
5. Sanity-check on your own tasks. Perplexity tables rank quants in
   general; your prompts are the only benchmark that includes your
   workload.

## Rules of thumb

- Q8_0 when memory is abundant: effectively indistinguishable from the
  16-bit original, at half its size.
- Q4_K_M as the default: the knee of the curve, where quality per byte
  is best for most models.
- Q5–Q6_K when the model is small relative to the budget: once a 7B
  fits easily, spend the slack on precision, not on a bigger quant of a
  model you did not want.
- Q2–Q3 only to make an otherwise impossible model load, and only with
  imatrix; expect visible damage and verify it on real prompts.
- Never buy bits you cannot afford by stealing them from the context:
  a Q6 with a cramped KV cache loses more than a Q4 with room to read.

## When the answer changes

Re-run the procedure when any input moves: a new GPU, a longer-context
task, a new model release, or a second concurrent conversation. The
procedure is cheap and the inputs drift — a quant chosen for last
year's card and a 4k context is routinely wrong for this year's card
and a 32k document workflow. The skill is not memorizing which quant
is best; it is being able to redo the arithmetic whenever the budget
changes.
