import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { Modal } from './ui'

/**
 * Lets the user frame an image before it is uploaded: drag to move it, zoom with the slider or the wheel,
 * arrows to nudge it. The image always covers the frame. Only the framed part is encoded, at outWidth ×
 * outHeight.
 */
export function ImageCropper({
  file,
  title,
  outWidth,
  outHeight,
  round,
  onCancel,
  onSave,
}: {
  file: File
  title: string
  outWidth: number
  outHeight: number
  round?: boolean
  onCancel: () => void
  onSave: (dataUrl: string) => Promise<void>
}) {
  const aspect = outWidth / outHeight
  const [img, setImg] = useState<HTMLImageElement | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const [frameW, setFrameW] = useState(0)
  const [zoom, setZoom] = useState(1)
  const [pos, setPos] = useState({ x: 0, y: 0 }) // image's top-left corner in the frame, in pixels
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null)

  useEffect(() => {
    const url = URL.createObjectURL(file)
    const i = new Image()
    i.onload = () => setImg(i)
    i.onerror = () => setError('Image illisible.')
    i.src = url
    return () => URL.revokeObjectURL(url)
  }, [file])

  useEffect(() => {
    const width = box.current?.clientWidth ?? 0
    setFrameW(Math.min(width, round ? 300 : 640))
  }, [img, round])

  const frameH = frameW / aspect
  const base = img ? Math.max(frameW / img.width, frameH / img.height) : 1
  const scale = base * zoom

  function clamp(p: { x: number; y: number }, s = scale) {
    if (!img) return p
    return {
      x: Math.min(0, Math.max(frameW - img.width * s, p.x)),
      y: Math.min(0, Math.max(frameH - img.height * s, p.y)),
    }
  }

  // Start centred once the image and the frame are known.
  useEffect(() => {
    if (!img || !frameW) return
    setZoom(1)
    setPos({ x: (frameW - img.width * base) / 2, y: (frameH - img.height * base) / 2 })
  }, [img, frameW])

  function setZoomAround(next: number) {
    const z = Math.min(4, Math.max(1, next))
    // Zoom around the frame's centre, so what is in the middle stays there.
    const s = base * z
    const cx = (frameW / 2 - pos.x) / scale
    const cy = (frameH / 2 - pos.y) / scale
    setZoom(z)
    setPos(clamp({ x: frameW / 2 - cx * s, y: frameH / 2 - cy * s }, s))
  }

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    e.currentTarget.setPointerCapture(e.pointerId)
    drag.current = { x: e.clientX, y: e.clientY, px: pos.x, py: pos.y }
  }

  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    const d = drag.current
    if (!d) return
    setPos(clamp({ x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y }))
  }

  function onKey(e: KeyboardEvent<HTMLDivElement>) {
    const step = e.shiftKey ? 40 : 10
    const moves: Record<string, [number, number]> = { ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step] }
    const m = moves[e.key]
    if (m) {
      e.preventDefault()
      setPos(clamp({ x: pos.x + m[0], y: pos.y + m[1] }))
    } else if (e.key === '+' || e.key === '=') {
      setZoomAround(zoom + 0.1)
    } else if (e.key === '-') {
      setZoomAround(zoom - 0.1)
    }
  }

  async function save() {
    if (!img) return
    setBusy(true)
    setError('')
    try {
      const canvas = document.createElement('canvas')
      canvas.width = outWidth
      canvas.height = outHeight
      canvas.getContext('2d')!.drawImage(img, -pos.x / scale, -pos.y / scale, frameW / scale, frameH / scale, 0, 0, outWidth, outHeight)
      const webp = canvas.toDataURL('image/webp', 0.85)
      // Browsers that cannot encode WebP give back a PNG: JPEG is lighter then.
      await onSave(webp.startsWith('data:image/webp') ? webp : canvas.toDataURL('image/jpeg', 0.85))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      title={title}
      subtitle="Faites glisser l’image pour la placer, zoomez avec le curseur."
      onClose={onCancel}
      wide={!round}
      footer={
        <>
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            Annuler
          </button>
          <button type="button" className="btn btn-primary" onClick={save} disabled={busy || !img}>
            {busy ? 'Enregistrement…' : 'Enregistrer'}
          </button>
        </>
      }
    >
      <div ref={box} className="cropper-box">
        {img && frameW > 0 && (
          <div
            className={`cropper-frame ${round ? 'cropper-round' : ''}`}
            style={{ width: frameW, height: frameH }}
            tabIndex={0}
            role="application"
            aria-label="Zone de cadrage : flèches pour déplacer, + et - pour zoomer"
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={() => (drag.current = null)}
            onPointerCancel={() => (drag.current = null)}
            onWheel={(e) => setZoomAround(zoom - e.deltaY * 0.002)}
            onKeyDown={onKey}
          >
            <img
              src={img.src}
              alt=""
              draggable={false}
              style={{ width: img.width * scale, height: img.height * scale, transform: `translate(${pos.x}px, ${pos.y}px)` }}
            />
          </div>
        )}
      </div>
      <label className="cropper-zoom">
        <span>Zoom</span>
        <input type="range" min={1} max={4} step={0.01} value={zoom} onChange={(e) => setZoomAround(Number(e.target.value))} disabled={!img} />
      </label>
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}
