// @vitest-environment happy-dom
import { describe, it, expect } from 'vitest'
import { dHash, hammingSimilarity } from './imageHash'

function solidImageData(width: number, height: number, r: number, g: number, b: number): ImageData {
  const data = new Uint8ClampedArray(width * height * 4)
  for (let i = 0; i < width * height; i++) {
    data[i * 4] = r
    data[i * 4 + 1] = g
    data[i * 4 + 2] = b
    data[i * 4 + 3] = 255
  }
  return new ImageData(data, width, height)
}

// Bright-left gradient: luma decreases left→right so every dHash bit is set (left > right).
// A solid image has all equal luma so no bits set (left == right, never >).
// Hamming distance = 64 bits → similarity = 0.0.
function brightLeftGradientImageData(width: number, height: number): ImageData {
  const data = new Uint8ClampedArray(width * height * 4)
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = (y * width + x) * 4
      // Bright on left, dark on right.
      const v = Math.round((1 - x / (width - 1)) * 255)
      data[i] = v
      data[i + 1] = v
      data[i + 2] = v
      data[i + 3] = 255
    }
  }
  return new ImageData(data, width, height)
}

describe('dHash', () => {
  it('is stable for a solid-colour image', () => {
    const img = solidImageData(64, 64, 255, 0, 0)
    const h1 = dHash(img)
    const h2 = dHash(img)
    expect(h1).toBe(h2)
  })

  it('two nearly-identical images have similarity >= 0.80', () => {
    // Red image, then the same but with a 5-pixel white horizontal line.
    const width = 64
    const height = 64
    const data = new Uint8ClampedArray(width * height * 4)
    for (let i = 0; i < width * height; i++) {
      data[i * 4] = 255
      data[i * 4 + 1] = 0
      data[i * 4 + 2] = 0
      data[i * 4 + 3] = 255
    }
    const base = new ImageData(new Uint8ClampedArray(data), width, height)

    // Draw a 5-pixel-wide white line at y=32.
    const modified = new Uint8ClampedArray(data)
    for (let y = 32; y < 37; y++) {
      for (let x = 0; x < width; x++) {
        const i = (y * width + x) * 4
        modified[i] = 255
        modified[i + 1] = 255
        modified[i + 2] = 255
        modified[i + 3] = 255
      }
    }
    const withLine = new ImageData(modified, width, height)

    const sim = hammingSimilarity(dHash(base), dHash(withLine))
    expect(sim).toBeGreaterThanOrEqual(0.80)
  })

  it('two visually distinct images have similarity < 0.55', () => {
    // Solid image: all luma equal → no left > right → hash = 0n (0 bits set).
    // Bright-left gradient: luma strictly decreasing left→right → every left > right
    // → all 64 bits set → hash = 0xFFFFFFFFFFFFFFFF.
    // Hamming distance = 64 → similarity = 0.0.
    const solid = solidImageData(64, 64, 128, 128, 128)
    const gradient = brightLeftGradientImageData(64, 64)
    const sim = hammingSimilarity(dHash(solid), dHash(gradient))
    expect(sim).toBeLessThan(0.55)
  })
})

describe('hammingSimilarity', () => {
  it('returns 1.0 for identical hashes', () => {
    const h = dHash(solidImageData(64, 64, 0, 128, 255))
    expect(hammingSimilarity(h, h)).toBe(1.0)
  })

  it('returns 0.0 for completely opposite hashes', () => {
    const allZero = 0n
    const allOne = (1n << 64n) - 1n
    expect(hammingSimilarity(allZero, allOne)).toBe(0.0)
  })
})
