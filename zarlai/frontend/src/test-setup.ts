/**
 * Vitest setup: polyfill browser APIs that jsdom/happy-dom don't implement,
 * needed by imageHash.ts (canvas 2D context, ImageData).
 */

// Minimal ImageData polyfill
if (typeof ImageData === 'undefined') {
  (globalThis as any).ImageData = class ImageData {
    readonly data: Uint8ClampedArray
    readonly width: number
    readonly height: number
    constructor(data: Uint8ClampedArray, width: number, height?: number) {
      this.data = data
      this.width = width
      this.height = height ?? data.length / 4 / width
    }
  }
}

// Minimal OffscreenCanvas-like in-memory pixel store used by our canvas stub.
class PixelBuffer {
  data: Uint8ClampedArray
  constructor(public width: number, public height: number) {
    this.data = new Uint8ClampedArray(width * height * 4)
  }
  getImageData(_x: number, _y: number, _w: number, _h: number): any {
    // For simplicity, return full buffer (our tests only call getImageData(0,0,w,h)).
    return new (globalThis as any).ImageData(new Uint8ClampedArray(this.data), this.width, this.height)
  }
  putImageData(imageData: any, dx: number, dy: number) {
    const src = imageData.data
    const sw = imageData.width
    const sh = imageData.height
    for (let sy = 0; sy < sh; sy++) {
      for (let sx = 0; sx < sw; sx++) {
        const si = (sy * sw + sx) * 4
        const di = ((dy + sy) * this.width + (dx + sx)) * 4
        if (di + 3 < this.data.length) {
          this.data[di] = src[si]
          this.data[di + 1] = src[si + 1]
          this.data[di + 2] = src[si + 2]
          this.data[di + 3] = src[si + 3]
        }
      }
    }
  }
  /** Scale-blit from a PixelBuffer source into this buffer. */
  drawFromBuffer(src: PixelBuffer, dw: number, dh: number) {
    for (let dy = 0; dy < dh; dy++) {
      for (let dx = 0; dx < dw; dx++) {
        const sx = Math.floor(dx * src.width / dw)
        const sy = Math.floor(dy * src.height / dh)
        const si = (sy * src.width + sx) * 4
        const di = (dy * this.width + dx) * 4
        this.data[di] = src.data[si]
        this.data[di + 1] = src.data[si + 1]
        this.data[di + 2] = src.data[si + 2]
        this.data[di + 3] = src.data[si + 3]
      }
    }
  }
}

// Patch document.createElement('canvas') to return a stub.
const _origCreateElement = document.createElement.bind(document)
;(document as any).createElement = function (tag: string, options?: any) {
  if (tag !== 'canvas') return _origCreateElement(tag, options)
  let buf: PixelBuffer | null = null
  const el: any = {
    _tag: 'canvas',
    width: 0,
    height: 0,
    _buf(): PixelBuffer {
      if (!buf || buf.width !== this.width || buf.height !== this.height) {
        buf = new PixelBuffer(this.width, this.height)
      }
      return buf
    },
    getContext(type: string, _opts?: any) {
      if (type !== '2d') return null
      const canvas = this
      return {
        drawImage(source: any, _dx: number, _dy: number, dw?: number, dh?: number) {
          const destW = dw ?? canvas.width
          const destH = dh ?? canvas.height
          // Identify the source pixel buffer.
          let srcBuf: PixelBuffer | null = null
          if (source._tag === 'canvas') {
            srcBuf = source._buf()
          }
          if (srcBuf) {
            canvas._buf().drawFromBuffer(srcBuf, destW, destH)
          }
        },
        putImageData(imageData: any, dx: number, dy: number) {
          canvas._buf().putImageData(imageData, dx, dy)
        },
        getImageData(x: number, y: number, w: number, h: number) {
          return canvas._buf().getImageData(x, y, w, h)
        },
      }
    },
  }
  return el
}
