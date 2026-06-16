import { useEffect, useRef } from 'react'
import * as THREE from 'three'
import type { SessionState } from '@/hooks/usePresenceSession'

interface Props {
  scene: THREE.Scene | null
  state: SessionState
}

const STATE_TINT: Record<SessionState, THREE.Color> = {
  loading: new THREE.Color(0xb8bcc5),
  listening: new THREE.Color(0x93c5fd),
  processing: new THREE.Color(0xa78bfa),
  speaking: new THREE.Color(0xf59e0b),
}

const STATE_ALPHA: Record<SessionState, number> = {
  loading: 0.2, listening: 0.45, processing: 0.5, speaking: 0.6,
}

const STATE_SPEED: Record<SessionState, number> = {
  loading: 1.0, listening: 1.0, processing: 1.1, speaking: 1.2,
}

const BASE_COUNT = 200
const EXTRA_SPEAKING = 60
const TOTAL = BASE_COUNT + EXTRA_SPEAKING
const BOUND_X = 4, BOUND_Y_MIN = 0, BOUND_Y_MAX = 3, BOUND_Z = 3

export default function ParticleField({ scene, state }: Props) {
  const stateRef = useRef(state)
  stateRef.current = state

  useEffect(() => {
    if (!scene) return

    const positions = new Float32Array(TOTAL * 3)
    const velocities = new Float32Array(TOTAL * 3)
    for (let i = 0; i < TOTAL; i++) {
      positions[i * 3 + 0] = (Math.random() - 0.5) * 2 * BOUND_X
      positions[i * 3 + 1] = BOUND_Y_MIN + Math.random() * (BOUND_Y_MAX - BOUND_Y_MIN)
      positions[i * 3 + 2] = (Math.random() - 0.5) * 2 * BOUND_Z
      velocities[i * 3 + 0] = (Math.random() - 0.5) * 0.1
      velocities[i * 3 + 1] = (Math.random() - 0.5) * 0.05
      velocities[i * 3 + 2] = (Math.random() - 0.5) * 0.1
    }

    const geometry = new THREE.BufferGeometry()
    geometry.setAttribute('position', new THREE.BufferAttribute(positions, 3))

    const material = new THREE.PointsMaterial({
      color: STATE_TINT.listening,
      sizeAttenuation: true,
      transparent: true,
      opacity: STATE_ALPHA.listening,
      depthWrite: false,
      size: 0.025,
    })

    const points = new THREE.Points(geometry, material)
    scene.add(points)

    let raf = 0
    let lastT = performance.now()
    function tick(now: number) {
      raf = requestAnimationFrame(tick)
      const dt = Math.min(0.1, (now - lastT) / 1000)
      lastT = now
      const s = stateRef.current
      const speed = STATE_SPEED[s]
      const count = s === 'speaking' ? TOTAL : BASE_COUNT
      for (let i = 0; i < count; i++) {
        const ix = i * 3
        positions[ix + 0] += velocities[ix + 0] * speed * dt + Math.sin(now * 0.0003 + i) * 0.0002
        positions[ix + 1] += velocities[ix + 1] * speed * dt
        positions[ix + 2] += velocities[ix + 2] * speed * dt
        if (positions[ix + 0] > BOUND_X) positions[ix + 0] = -BOUND_X
        else if (positions[ix + 0] < -BOUND_X) positions[ix + 0] = BOUND_X
        if (positions[ix + 1] > BOUND_Y_MAX) positions[ix + 1] = BOUND_Y_MIN
        else if (positions[ix + 1] < BOUND_Y_MIN) positions[ix + 1] = BOUND_Y_MAX
        if (positions[ix + 2] > BOUND_Z) positions[ix + 2] = -BOUND_Z
        else if (positions[ix + 2] < -BOUND_Z) positions[ix + 2] = BOUND_Z
      }
      geometry.setDrawRange(0, count)
      geometry.attributes.position.needsUpdate = true
      material.color.copy(STATE_TINT[s])
      material.opacity += (STATE_ALPHA[s] - material.opacity) * 0.1
    }
    raf = requestAnimationFrame(tick)

    return () => {
      cancelAnimationFrame(raf)
      scene.remove(points)
      geometry.dispose()
      material.dispose()
    }
  }, [scene])

  return null
}
