/**
 * dHash computes a 64-bit difference hash of an image source.
 * Algorithm: downscale to 9×8 grayscale, then set bit i if the left
 * neighbour is brighter than the right neighbour across the 8×8
 * horizontal-diff grid. Returns a bigint (64 bits). Fast (~1ms) and
 * robust to minor lighting and compression changes.
 */
export function dHash(source: HTMLCanvasElement | HTMLVideoElement | ImageData): bigint {
  const w = 9
  const h = 8
  const c = document.createElement('canvas')
  c.width = w
  c.height = h
  const ctx = c.getContext('2d', { willReadFrequently: true })
  if (!ctx) throw new Error('dHash: 2d context unavailable')
  if (source instanceof ImageData) {
    // Draw ImageData into an intermediate canvas, then scale it down.
    const src = document.createElement('canvas')
    src.width = source.width
    src.height = source.height
    src.getContext('2d')!.putImageData(source, 0, 0)
    ctx.drawImage(src, 0, 0, w, h)
  } else {
    ctx.drawImage(source, 0, 0, w, h)
  }
  const data = ctx.getImageData(0, 0, w, h).data
  // Convert to grayscale (luma 0.299R + 0.587G + 0.114B) into a 9×8 array.
  const lum: number[] = new Array(w * h)
  for (let i = 0; i < w * h; i++) {
    const r = data[i * 4]
    const g = data[i * 4 + 1]
    const b = data[i * 4 + 2]
    lum[i] = 0.299 * r + 0.587 * g + 0.114 * b
  }
  let hash = 0n
  let bit = 0
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w - 1; x++) {
      const left = lum[y * w + x]
      const right = lum[y * w + x + 1]
      if (left > right) hash |= 1n << BigInt(bit)
      bit++
    }
  }
  return hash
}

/**
 * hammingSimilarity returns a value in [0, 1] where 1 means "identical
 * hashes" and 0 means "every bit differs". Two JPEGs of the same scene
 * with minor motion/lighting typically score ≥0.90; distinct scenes
 * score ≤0.70.
 */
export function hammingSimilarity(a: bigint, b: bigint): number {
  let x = a ^ b
  let bits = 0
  while (x !== 0n) {
    bits += Number(x & 1n)
    x >>= 1n
  }
  return 1 - bits / 64
}
