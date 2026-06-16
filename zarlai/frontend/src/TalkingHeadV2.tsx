import { useRef, useEffect } from 'react'
import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'

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

export default function TalkingHeadV2({ analyser, state }: TalkingHeadProps) {
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
    camera.position.set(0, 1.77, 1.0)
    camera.lookAt(0, 1.72, 0)

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

    // State
    let headMesh: THREE.SkinnedMesh | null = null
    let morphDict: Record<string, number> = {}
    let headBone: THREE.Bone | null = null
    const syncMeshes: THREE.SkinnedMesh[] = [] // teeth, tongue, eyeAO, eyelash, eye

    // Morph helper: set morph by name on head mesh
    function setMorph(val: number, ...names: string[]) {
      if (!headMesh?.morphTargetInfluences) return
      for (const name of names) {
        const idx = morphDict[name]
        if (idx !== undefined) headMesh.morphTargetInfluences[idx] = val
      }
    }

    // Sync all secondary meshes with head — they share morph target names
    function syncAll() {
      if (!headMesh?.morphTargetInfluences) return
      for (const mesh of syncMeshes) {
        if (!mesh.morphTargetInfluences || !mesh.morphTargetDictionary) continue
        for (const [name, idx] of Object.entries(mesh.morphTargetDictionary)) {
          const headIdx = morphDict[name]
          if (headIdx !== undefined) {
            mesh.morphTargetInfluences[idx] = headMesh.morphTargetInfluences[headIdx]
          }
        }
      }
    }

    // Viseme names in order — Oculus viseme set
    const VISEMES = [
      'viseme_sil', 'viseme_PP', 'viseme_FF', 'viseme_TH',
      'viseme_DD', 'viseme_kk', 'viseme_CH', 'viseme_SS',
      'viseme_nn', 'viseme_RR', 'viseme_aa', 'viseme_E',
      'viseme_I', 'viseme_O', 'viseme_U',
    ]

    // Smoothed viseme weights for crossfading
    const visemeWeights = new Float32Array(VISEMES.length)

    // Eye gaze state
    let gazeTargetX = 0
    let gazeTargetY = 0
    let gazeCurrentX = 0
    let gazeCurrentY = 0
    let nextGazeShift = 0

    // Load RPM model
    const loader = new GLTFLoader()
    loader.load('/models/avaturn.glb', (gltf) => {
      const model = gltf.scene

      model.traverse((child) => {
        if (!(child as THREE.SkinnedMesh).isSkinnedMesh) {
          if ((child as THREE.Bone).isBone && child.name === 'Head') {
            headBone = child as THREE.Bone
          }
          return
        }
        const mesh = child as THREE.SkinnedMesh

        // Head mesh is the primary — all others sync their shared morphs from it
        const isHead = mesh.name === 'Head_Mesh' || mesh.name === 'Wolf3D_Head'

        if (isHead) {
          headMesh = mesh
          if (mesh.morphTargetDictionary && mesh.morphTargetInfluences) {
            morphDict = mesh.morphTargetDictionary
          }
        } else if (mesh.morphTargetDictionary && mesh.morphTargetInfluences) {
          // Teeth, tongue, eyelash, eyeAO, eye — all sync from head
          syncMeshes.push(mesh)
        }

        // Only show head-related meshes
        const headMeshes = [
          'Head_Mesh', 'Teeth_Mesh', 'Tongue_Mesh', 'Eye_Mesh',
          'EyeAO_Mesh', 'Eyelash_Mesh',
          'Wolf3D_Head', 'Wolf3D_Teeth', 'EyeLeft', 'EyeRight',
          'Wolf3D_Hair', 'Wolf3D_Glasses',
          'avaturn_hair_0', 'avaturn_hair_1',
        ]
        if (!headMeshes.includes(mesh.name)) {
          mesh.visible = false
        }
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

      // Subtle head sway
      if (headBone) {
        headBone.rotation.y = Math.sin(time * 0.3) * 0.015
        headBone.rotation.x = Math.sin(time * 0.2) * 0.008
      }

      // Eye gaze via morph targets (eyeLookUp/Down/In/Out)
      if (headMesh?.morphTargetInfluences) {
        if (time > nextGazeShift) {
          gazeTargetX = (Math.random() - 0.5) * 0.5
          gazeTargetY = (Math.random() - 0.5) * 0.3
          nextGazeShift = time + 1 + Math.random() * 3
        }

        gazeCurrentX += (gazeTargetX - gazeCurrentX) * 0.03
        gazeCurrentY += (gazeTargetY - gazeCurrentY) * 0.03

        // Micro-saccades
        const sx = (Math.sin(time * 7.3) + Math.sin(time * 11.1)) * 0.02
        const sy = (Math.sin(time * 8.9) + Math.sin(time * 13.7)) * 0.02
        const gx = gazeCurrentX + sx
        const gy = gazeCurrentY + sy

        // Horizontal: positive = look right, negative = look left
        // eyeLookInLeft = look toward nose (right for left eye)
        // eyeLookOutLeft = look away from nose (left for left eye)
        setMorph(Math.max(0, gx), 'eyeLookInLeft', 'eyeLookOutRight')
        setMorph(Math.max(0, -gx), 'eyeLookOutLeft', 'eyeLookInRight')
        // Vertical
        setMorph(Math.max(0, gy), 'eyeLookUpLeft', 'eyeLookUpRight')
        setMorph(Math.max(0, -gy), 'eyeLookDownLeft', 'eyeLookDownRight')
      }

      // Audio-driven visemes via frequency analysis
      if (headMesh?.morphTargetInfluences) {
        // Target viseme weights — computed from audio, then smoothed
        const targetWeights = new Float32Array(VISEMES.length)

        if (an && s === 'speaking') {
          // Frequency spectrum analysis
          const freqData = new Float32Array(an.frequencyBinCount)
          an.getFloatFrequencyData(freqData) // dB values

          // Compute energy in frequency bands (convert dB to linear)
          const binCount = freqData.length
          const band1End = Math.floor(binCount * 0.08)  // ~0-500Hz: low
          const band2End = Math.floor(binCount * 0.25)  // ~500-2kHz: mid
          const band3End = Math.floor(binCount * 0.5)   // ~2k-4kHz: high
          let low = 0, mid = 0, high = 0, total = 0

          for (let i = 1; i < band3End; i++) {
            const energy = Math.pow(10, (freqData[i] + 100) / 40) // normalise from dB
            if (i < band1End) low += energy
            else if (i < band2End) mid += energy
            else high += energy
            total += energy
          }

          // RMS for overall amplitude
          const timeBuf = new Float32Array(an.fftSize)
          an.getFloatTimeDomainData(timeBuf)
          let rmsSum = 0
          for (let j = 0; j < timeBuf.length; j++) rmsSum += timeBuf[j] * timeBuf[j]
          const rms = Math.sqrt(rmsSum / timeBuf.length)
          const amplitude = Math.min(rms * 6, 1.0)

          if (amplitude > 0.02) {
            const norm = total + 0.001
            const lowR = low / norm
            const midR = mid / norm
            const highR = high / norm

            // Map frequency profile to viseme weights
            // Low frequencies → open vowels
            targetWeights[10] = lowR * 0.8 * amplitude  // viseme_aa
            targetWeights[13] = lowR * 0.4 * amplitude  // viseme_O
            // Mid frequencies → mid vowels and consonants
            targetWeights[11] = midR * 0.6 * amplitude  // viseme_E
            targetWeights[12] = midR * 0.4 * amplitude  // viseme_I
            targetWeights[4]  = midR * 0.3 * amplitude  // viseme_DD
            targetWeights[8]  = midR * 0.3 * amplitude  // viseme_nn
            // High frequencies → fricatives
            targetWeights[7]  = highR * 0.6 * amplitude // viseme_SS
            targetWeights[2]  = highR * 0.3 * amplitude // viseme_FF
            targetWeights[3]  = highR * 0.2 * amplitude // viseme_TH
            targetWeights[6]  = highR * 0.3 * amplitude // viseme_CH
            // Plosives from transients (high amplitude + mid energy)
            targetWeights[1]  = Math.max(0, amplitude - 0.5) * midR * 0.5 // viseme_PP
            targetWeights[5]  = Math.max(0, amplitude - 0.4) * midR * 0.3 // viseme_kk
            // Back vowel from low-mid
            targetWeights[14] = lowR * midR * amplitude * 0.5 // viseme_U
            targetWeights[9]  = midR * lowR * amplitude * 0.4 // viseme_RR
          } else {
            targetWeights[0] = 0.3 // viseme_sil — slight closed mouth
          }
        }

        // Smooth crossfade all viseme weights
        const lerpSpeed = 0.15
        for (let i = 0; i < VISEMES.length; i++) {
          visemeWeights[i] += (targetWeights[i] - visemeWeights[i]) * lerpSpeed
          setMorph(visemeWeights[i], VISEMES[i])
        }

        // Jaw driven by sum of open visemes
        const jawTarget = (visemeWeights[10] + visemeWeights[13] + visemeWeights[11]) * 0.8
        smoothJaw += (jawTarget - smoothJaw) * 0.2
        setMorph(smoothJaw, 'jawOpen')

        // Blink
        const blinkWave = Math.sin(time * 0.4) + Math.sin(time * 0.67)
        const blinkVal = blinkWave > 1.85 ? 1.0 : 0.0
        setMorph(blinkVal, 'eyeBlinkLeft', 'eyeBlinkRight')

        // Brow raise when processing
        setMorph(s === 'processing' ? 0.3 + Math.sin(time * 2) * 0.1 : 0, 'browInnerUp')

        // Slight smile when listening
        if (s !== 'speaking') {
          setMorph(s === 'listening' ? 0.15 : 0, 'mouthSmileLeft', 'mouthSmileRight')
        }

        // Idle mouth fidget when not speaking
        if (s !== 'speaking') {
          const lipPress = Math.max(0, Math.sin(time * 0.9) * 0.25)
          setMorph(lipPress, 'mouthPressLeft', 'mouthPressRight')

          const shift = Math.sin(time * 0.4) * 0.1
          setMorph(Math.max(0, shift), 'mouthLeft')
          setMorph(Math.max(0, -shift), 'mouthRight')

          const rollVal = Math.max(0, Math.sin(time * 0.7 + 2) * 0.15)
          setMorph(rollVal, 'mouthRollLower')
        }

        // Sync all secondary meshes (teeth, tongue, eyelash, eyeAO, eye)
        syncAll()
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
