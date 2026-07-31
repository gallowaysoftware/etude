# Reading Quantization Names

## Decoding Q4_K_M

Quantization filenames pack their recipe into a short code, and the
code is read in three parts. Take the most common one on any download
page, Q4_K_M:

- Q4: the nominal bit width. Four bits per weight before overhead —
  roughly half a byte per parameter.
- K: the k-quants, the block-encoding family used by llama.cpp's modern
  files. K-quants group blocks into superblocks that share a
  higher-precision scale, squeezing the scale overhead that simpler
  schemes pay per block. Older files with no letter in this position
  (Q4_0, Q5_1) use flat per-block scales and are kept for
  compatibility, not chosen on purpose.
- M: the size mix — small, medium, or large. Not every tensor gets the
  nominal width. The tensors the model is most sensitive to, chiefly
  the embedding table and the output projection, are stored a bit or
  two higher, and M/S/L selects how generous that exception budget is.
  A Q4_K_M file therefore averages about 4.8 bits per weight rather
  than exactly 4.

## The other families you will meet

Two extensions of the same scheme appear beside the k-quants. I-quants
(Q4_K's newer cousins such as IQ4_XS) derive the codebook from the
weight distribution itself instead of a uniform grid, recovering
quality at very low bit widths at the cost of slower dequantization on
some hardware. Files tagged "imatrix" were quantized against an
importance matrix: a measurement of which weights actually matter,
gathered by running calibration text through the model, so the
quantizer spends its precision where rounding would hurt most. Imatrix
quants are the current answer to usable 2- and 3-bit files.

## Numbers worth memorizing

The nominal bit width tells you the storage class; the average width
tells you the file size. In practice three rows of the table do most of
the work: Q8_0 at about 8.5 bits per weight (effectively lossless, for
when memory is abundant), Q4_K_M at about 4.8 (the default compromise),
and Q2–Q3 imatrix files at 2.5–3.5 (the floor, where quality damage is
real but the file fits where nothing else would). Everything else is an
interpolation between those anchors, chosen when the memory budget
falls between them.
