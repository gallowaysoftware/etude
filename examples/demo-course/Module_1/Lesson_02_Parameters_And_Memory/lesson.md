# Parameters and the Memory They Take

## The weight-memory rule

The single most useful number in home inference is the memory the
weights occupy, and it obeys one rule:

memory for weights ≈ parameter count × bytes per parameter

The parameter count is in the model's name — 7B means roughly seven
billion. The bytes per parameter depend on the numeric type the weights
are stored in:

- 16-bit (FP16 or BF16): 2 bytes per parameter, so 7B ≈ 14 GB.
- 8-bit quantized: 1 byte per parameter, so 7B ≈ 7 GB.
- 4-bit quantized: about half a byte per parameter, so 7B ≈ 3.5–4 GB.

The 4-bit row is approximate because real quantizers never hit the
nominal bit count exactly, which is why this rule is a budgeting tool
and not an exact measurement.

## Why real files are larger than the rule

A quantized file stores more than the packed weight bits. Quantization
works on blocks of values, and every block carries its own scale factor
— a small floating-point number the loader multiplies the block by when
dequantizing. Those scales, plus per-tensor metadata and the tensors
kept at higher precision (embedding tables and the final output
projection are usually stored at 6 or 8 bits even in a 4-bit file), push
the real file 5–15% above the nominal figure. When a "4-bit" 7B file
downloads at 4.1 GB rather than 3.5 GB, that gap is the scales and the
high-precision exceptions, not an error.

## Weights are only the first line of the budget

The weight rule tells you the size of the file, not the size of the run.
A running model also allocates the KV cache (covered in the next
lesson), compute buffers for the intermediate activations of the tokens
being processed right now, and whatever the operating system and desktop
were already using. The weight figure is the largest single term and the
only one you know before loading, so it is where budgeting starts — but
a model whose weights exactly equal your VRAM is a model that does not
fit.

## Reading a model card

Model pages list parameter count, available quantizations, and file
sizes. The file size column is the weight memory already multiplied out
for you; the parameter count is what you use to reason about quants that
aren't listed. Both numbers are about the weights alone. Everything else
— context length, offload split, batch size — moves the total from
there, which is why two people can report opposite "it fits" results for
the same file on the same card.
