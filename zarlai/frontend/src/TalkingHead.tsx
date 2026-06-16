import { useRef, useEffect } from 'react'
import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { MeshoptDecoder } from 'meshoptimizer'

interface TalkingHeadProps {
  analyser: AnalyserNode | null
  state: 'loading' | 'listening' | 'processing' | 'speaking'
}

const STATE_COLOR: Record<string, THREE.Color> = {
  loading: new THREE.Color(0x3a3d46),
  listening: new THREE.Color(0x93c5fd),
  processing: new THREE.Color(0xa78bfa),
  speaking: new THREE.Color(0xf59e0b),
}

export default function TalkingHead({ analyser, state }: TalkingHeadProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const analyserRef = useRef(analyser)
  const stateRef = useRef(state)
  analyserRef.current = analyser
  stateRef.current = state

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const w = container.clientWidth
    const h = container.clientHeight

    const scene = new THREE.Scene()
    const camera = new THREE.PerspectiveCamera(20, w / h, 0.1, 100)
    camera.position.set(0, 0.5, 8.5)
    camera.lookAt(0, 0, 0)

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true })
    renderer.setSize(w, h)
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    renderer.setClearColor(0x000000, 0)
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.2
    container.appendChild(renderer.domElement)

    // Lighting
    scene.add(new THREE.AmbientLight(0xffffff, 0.7))
    const key = new THREE.DirectionalLight(0xffffff, 1.2)
    key.position.set(2, 3, 4)
    scene.add(key)
    const fill = new THREE.DirectionalLight(0x93c5fd, 0.6)
    fill.position.set(-2, 1, 3)
    scene.add(fill)
    const rim = new THREE.DirectionalLight(0xf59e0b, 0.4)
    rim.position.set(0, 2, -3)
    scene.add(rim)

    // Materials
    const skinMat = new THREE.MeshStandardMaterial({ color: 0xc4956a, roughness: 0.75, metalness: 0.02 })
    const eyeMat = new THREE.MeshStandardMaterial({ color: 0xf5f5f0, roughness: 0.15, metalness: 0.02 })
    const teethMat = new THREE.MeshStandardMaterial({ color: 0xf0ebe5, roughness: 0.4, metalness: 0, side: THREE.DoubleSide })

    // State
    let faceMesh: THREE.Mesh | null = null
    let morphDict: Record<string, number> = {}
    let headNode: THREE.Object3D | null = null
    let eyeLeftGrp: THREE.Object3D | null = null
    let eyeRightGrp: THREE.Object3D | null = null

    // Eye gaze state — natural idle movement
    let gazeTargetX = 0
    let gazeTargetZ = 0
    let gazeCurrentX = 0
    let gazeCurrentZ = 0
    let nextGazeShift = 0

    // Load model
    const loader = new GLTFLoader()
    loader.setMeshoptDecoder(MeshoptDecoder)
    loader.load('/models/facecap.glb', (gltf) => {
      const model = gltf.scene
      model.position.set(0, -0.3, 0)

      // Assign materials by parent node name
      model.traverse((child) => {
        if (!(child as THREE.Mesh).isMesh) {
          if (child.name === 'head') headNode = child
          if (child.name === 'grp_eyeLeft') eyeLeftGrp = child
          if (child.name === 'grp_eyeRight') eyeRightGrp = child
          return
        }
        const mesh = child as THREE.Mesh
        const parentName = mesh.parent?.name || ''

        if (parentName === 'eyeLeft' || parentName === 'eyeRight') {
          mesh.material = eyeMat
        } else if (parentName === 'teeth') {
          mesh.material = teethMat
        } else if (parentName === 'head') {
          mesh.material = skinMat
          if (mesh.morphTargetDictionary && mesh.morphTargetInfluences) {
            faceMesh = mesh
            morphDict = mesh.morphTargetDictionary
          }
        } else {
          mesh.material = skinMat
        }
      })

      // Dark mouth cavity — a sphere behind the teeth simulates the
      // dark throat/interior. This model has no separate mouth geometry,
      // so a cavity prop is the standard game-dev approach.
      const teethNode = model.getObjectByName('teeth')
      if (teethNode) {
        const tm = teethNode.children[0] as THREE.Mesh | undefined
        if (tm?.geometry) {
          tm.geometry.computeBoundingSphere()
          const tbs = tm.geometry.boundingSphere!
          const cavity = new THREE.Mesh(
            new THREE.SphereGeometry(tbs.radius * 0.8, 16, 12),
            new THREE.MeshBasicMaterial({ color: 0x2a0810 }),
          )
          cavity.position.set(tbs.center.x, tbs.center.y - tbs.radius * 0.5, tbs.center.z)
          cavity.scale.set(1.4, 0.7, 1.1)
          tm.add(cavity)
        }
      }

      // Add iris, pupil, and highlight to each eye mesh dynamically.
      // The model has a 90° X rotation in grp_scale, so the front of
      // the eye (toward camera) is +Y in mesh local space, not +Z.
      // CircleGeometry faces +Z by default, so rotate -90° around X.
      model.traverse((node) => {
        if (!(node as THREE.Mesh).isMesh) return
        const m = node as THREE.Mesh
        const pn = m.parent?.name || ''
        if (pn !== 'eyeLeft' && pn !== 'eyeRight') return

        m.geometry.computeBoundingSphere()
        const bs = m.geometry.boundingSphere!
        const c = bs.center
        const r = bs.radius

        // Iris — coloured ring on front surface of eyeball
        const iris = new THREE.Mesh(
          new THREE.RingGeometry(r * 0.12, r * 0.3, 32),
          new THREE.MeshBasicMaterial({ color: 0x3a7ca5, side: THREE.DoubleSide }),
        )
        iris.position.set(c.x, c.y + r * 0.96, c.z)
        iris.rotation.x = -Math.PI / 2
        m.add(iris)

        // Pupil — dark centre
        const pupil = new THREE.Mesh(
          new THREE.CircleGeometry(r * 0.12, 24),
          new THREE.MeshBasicMaterial({ color: 0x0f0f0f }),
        )
        pupil.position.set(c.x, c.y + r * 0.97, c.z)
        pupil.rotation.x = -Math.PI / 2
        m.add(pupil)

        // Specular highlight — small white dot for life
        const highlight = new THREE.Mesh(
          new THREE.CircleGeometry(r * 0.05, 12),
          new THREE.MeshBasicMaterial({ color: 0xffffff }),
        )
        highlight.position.set(c.x + r * 0.08, c.y + r * 0.98, c.z + r * 0.1)
        highlight.rotation.x = -Math.PI / 2
        m.add(highlight)
      })

      scene.add(model)
    })

    // Animate
    let time = 0
    let rafId = 0
    let smoothJaw = 0

    function animate() {
      rafId = requestAnimationFrame(animate)
      time += 0.016

      const s = stateRef.current
      const an = analyserRef.current

      // Very subtle head sway
      if (headNode) {
        headNode.rotation.y = Math.sin(time * 0.3) * 0.015
        headNode.rotation.x = Math.sin(time * 0.2) * 0.008
      }

      // Eye gaze — natural idle movement
      // Periodically pick a new gaze target, smoothly drift toward it,
      // with constant micro-saccades layered on top.
      if (eyeLeftGrp || eyeRightGrp) {
        // Pick a new gaze target every 1-4 seconds
        if (time > nextGazeShift) {
          gazeTargetX = (Math.random() - 0.5) * 0.08
          gazeTargetZ = (Math.random() - 0.5) * 0.05
          nextGazeShift = time + 1 + Math.random() * 3
        }

        // Smooth drift toward target
        gazeCurrentX += (gazeTargetX - gazeCurrentX) * 0.03
        gazeCurrentZ += (gazeTargetZ - gazeCurrentZ) * 0.03

        // Micro-saccades: tiny rapid involuntary jitter
        const saccadeX = (Math.sin(time * 7.3) + Math.sin(time * 11.1)) * 0.002
        const saccadeZ = (Math.sin(time * 8.9) + Math.sin(time * 13.7)) * 0.002

        for (const eye of [eyeLeftGrp, eyeRightGrp]) {
          if (!eye) continue
          eye.rotation.y = gazeCurrentX + saccadeX
          eye.rotation.x = gazeCurrentZ + saccadeZ
        }
      }

      // Audio-driven mouth
      let targetJaw = 0
      let viseme = 0

      if (an && s === 'speaking') {
        const buf = new Float32Array(an.fftSize)
        an.getFloatTimeDomainData(buf)
        let sum = 0
        for (let j = 0; j < buf.length; j++) sum += buf[j] * buf[j]
        const rms = Math.sqrt(sum / buf.length)
        targetJaw = Math.min(rms * 5, 0.5)

        if (rms > 0.02) {
          const a = Math.sin(time * 5.7) + Math.sin(time * 3.3)
          if (a > 0.8) viseme = 1
          else if (a > -0.3) viseme = 2
          else if (a > -1.2) viseme = 3
          else viseme = 0
        }
      }

      smoothJaw += (targetJaw - smoothJaw) * 0.25

      if (faceMesh && faceMesh.morphTargetInfluences) {
        // Reset mouth shapes
        const mouthTargets = ['jawOpen', 'mouthOpen', 'mouthFunnel', 'mouthPucker',
          'mouthStretch_L', 'mouthStretch_R', 'mouthSmile_L', 'mouthSmile_R',
          'mouthClose', 'mouthUpperUp_L', 'mouthUpperUp_R', 'mouthLowerDown_L', 'mouthLowerDown_R',
          'mouthPress_L', 'mouthPress_R', 'mouthLeft', 'mouthRight', 'mouthRollLower', 'jawForward']
        for (const name of mouthTargets) {
          const idx = morphDict[name]
          if (idx !== undefined) faceMesh.morphTargetInfluences[idx] = 0
        }

        // Jaw
        const jawIdx = morphDict['jawOpen']
        if (jawIdx !== undefined) faceMesh.morphTargetInfluences[jawIdx] = smoothJaw

        // Viseme shapes
        const intensity = smoothJaw * 0.6
        if (viseme === 1) {
          const ul = morphDict['mouthUpperUp_L']
          const ur = morphDict['mouthUpperUp_R']
          const ll = morphDict['mouthLowerDown_L']
          const lr = morphDict['mouthLowerDown_R']
          if (ul !== undefined) faceMesh.morphTargetInfluences[ul] = intensity * 0.4
          if (ur !== undefined) faceMesh.morphTargetInfluences[ur] = intensity * 0.4
          if (ll !== undefined) faceMesh.morphTargetInfluences[ll] = intensity * 0.4
          if (lr !== undefined) faceMesh.morphTargetInfluences[lr] = intensity * 0.4
        } else if (viseme === 2) {
          const l = morphDict['mouthStretch_L']
          const r = morphDict['mouthStretch_R']
          if (l !== undefined) faceMesh.morphTargetInfluences[l] = intensity * 0.4
          if (r !== undefined) faceMesh.morphTargetInfluences[r] = intensity * 0.4
          const sl = morphDict['mouthSmile_L']
          const sr = morphDict['mouthSmile_R']
          if (sl !== undefined) faceMesh.morphTargetInfluences[sl] = intensity * 0.3
          if (sr !== undefined) faceMesh.morphTargetInfluences[sr] = intensity * 0.3
        } else if (viseme === 3) {
          const idx = morphDict['mouthFunnel']
          if (idx !== undefined) faceMesh.morphTargetInfluences[idx] = intensity * 0.4
          const pk = morphDict['mouthPucker']
          if (pk !== undefined) faceMesh.morphTargetInfluences[pk] = intensity * 0.25
        }

        // Blink — sparse, irregular
        const blinkWave = Math.sin(time * 0.4) + Math.sin(time * 0.67)
        const blinkVal = blinkWave > 1.85 ? 1.0 : 0.0
        const blinkL = morphDict['eyeBlink_L']
        const blinkR = morphDict['eyeBlink_R']
        if (blinkL !== undefined) faceMesh.morphTargetInfluences[blinkL] = blinkVal
        if (blinkR !== undefined) faceMesh.morphTargetInfluences[blinkR] = blinkVal

        // Brow raise when processing
        const browUp = morphDict['browInnerUp']
        if (browUp !== undefined) {
          faceMesh.morphTargetInfluences[browUp] = s === 'processing' ? 0.3 + Math.sin(time * 2) * 0.1 : 0
        }

        // Slight smile when listening
        if (s !== 'speaking') {
          const smileL = morphDict['mouthSmile_L']
          const smileR = morphDict['mouthSmile_R']
          if (smileL !== undefined) faceMesh.morphTargetInfluences[smileL] = s === 'listening' ? 0.15 : 0
          if (smileR !== undefined) faceMesh.morphTargetInfluences[smileR] = s === 'listening' ? 0.15 : 0
        }

        // Idle mouth fidget when not speaking
        if (s !== 'speaking') {
          const pressL = morphDict['mouthPress_L']
          const pressR = morphDict['mouthPress_R']
          const lipPress = Math.max(0, Math.sin(time * 0.9) * 0.25)
          if (pressL !== undefined) faceMesh.morphTargetInfluences[pressL] = lipPress
          if (pressR !== undefined) faceMesh.morphTargetInfluences[pressR] = lipPress

          const mouthLeft = morphDict['mouthLeft']
          const mouthRight = morphDict['mouthRight']
          const shift = Math.sin(time * 0.4) * 0.1
          if (mouthLeft !== undefined) faceMesh.morphTargetInfluences[mouthLeft] = Math.max(0, shift)
          if (mouthRight !== undefined) faceMesh.morphTargetInfluences[mouthRight] = Math.max(0, -shift)

          const rollLower = morphDict['mouthRollLower']
          const rollVal = Math.max(0, Math.sin(time * 0.7 + 2) * 0.15)
          if (rollLower !== undefined) faceMesh.morphTargetInfluences[rollLower] = rollVal
        }
      }

      // State-driven lighting
      const stateColor = STATE_COLOR[s] ?? STATE_COLOR.listening
      fill.color.lerp(stateColor, 0.05)

      renderer.render(scene, camera)
    }
    animate()

    const ro = new ResizeObserver(() => {
      const nw = container.clientWidth
      const nh = container.clientHeight
      renderer.setSize(nw, nh)
      camera.aspect = nw / nh
      camera.updateProjectionMatrix()
    })
    ro.observe(container)

    return () => {
      cancelAnimationFrame(rafId)
      ro.disconnect()
      renderer.dispose()
      if (container.contains(renderer.domElement)) container.removeChild(renderer.domElement)
    }
  }, [])

  return <div ref={containerRef} className="w-full h-full" />
}
