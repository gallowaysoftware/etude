# What Quantization Is

## The idea

Quantization stores a model's weights at lower numeric precision than
the model was trained at. A weight that training kept as a 16-bit
float — two bytes holding a wide range with fine resolution — is
re-expressed as a 4-bit or 8-bit code. At 4 bits, a billion parameters
shrink from 2 GB to roughly half a gigabyte, which is the entire reason
home inference of large models is practical. The computation itself
does not run on 4-bit numbers: the loader expands each code back to a
16-bit float as the matrix multiplication consumes it. Quantization
changes how weights are stored and moved, not the precision of the
arithmetic that uses them.

## Block-wise encoding

Naive rounding of every weight to the nearest 4-bit value would be
brutal, because weights span a range far wider than 16 discrete steps.
Real schemes therefore work in blocks. A run of 32 or 256 consecutive
weights shares one scale factor — and often a zero point — stored as a
small float. Each weight in the block becomes a small integer code:
dequantized value ≈ code × scale. The scale adapts the coarse codes to
each block's actual range, which is why block-wise 4-bit storage loses
far less than a global 4-bit grid would. It is also why a quantized
file is larger than the nominal bit count predicts: every block carries
its scale along with its codes.

## What the error looks like

The rounding error from quantization is not random noise sprinkled on
top of the model's behavior; it is a small, fixed perturbation of every
weight. Most weights tolerate it — neural networks are trained amid
noise and their outputs are insensitive to tiny per-weight changes. But
the errors accumulate across the hundreds of matrix multiplications a
token passes through, and they concentrate where the model was already
uncertain. The practical signature is not gibberish but a slow drift:
slightly flatter phrasing, slightly weaker recall of rare facts,
slightly shorter chains of reliable reasoning. The next lessons cover
how big that drift is at each bit width and how to shop under it.

## What quantization does not touch

Only the stored weights are quantized by default. The KV cache stays at
full precision unless you ask otherwise (a separate option with its own
costs, covered later), the activations in flight are always full
precision, and the model's architecture — layers, heads, context length
— is identical to the unquantized original. A quantized model is the
same model wearing smaller luggage.
