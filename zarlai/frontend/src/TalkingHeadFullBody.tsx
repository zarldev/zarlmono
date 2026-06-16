import { useRef, useEffect } from 'react'
import * as THREE from 'three'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import type { SessionState } from './hooks/usePresenceSession'

interface Props {
  analyser: AnalyserNode | null
  state: SessionState
  onSceneReady?: (scene: THREE.Scene) => void
}

export default function TalkingHeadFullBody({ analyser, state, onSceneReady }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const analyserRef = useRef(analyser)
  const stateRef = useRef(state)
  analyserRef.current = analyser
  stateRef.current = state

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    const el: HTMLDivElement = container

    const w = el.clientWidth
    const h = el.clientHeight

    const scene = new THREE.Scene()
    onSceneReady?.(scene)
    // Full-body framing: camera at chest height, 3.5m back, telephoto feel
    const camera = new THREE.PerspectiveCamera(32, w / h, 0.1, 100)
    camera.position.set(0, 1.1, 4.3)
    camera.lookAt(0, 0.95, 0)

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true })
    renderer.setSize(w, h)
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    renderer.setClearColor(0x000000, 0)
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.15
    renderer.shadowMap.enabled = true
    renderer.shadowMap.type = THREE.PCFSoftShadowMap
    el.appendChild(renderer.domElement)

    scene.add(new THREE.AmbientLight(0xffffff, 0.6))
    const key = new THREE.DirectionalLight(0xffffff, 1.1)
    key.position.set(2, 3, 4)
    key.castShadow = true
    key.shadow.mapSize.set(1024, 1024)
    scene.add(key)
    const fill = new THREE.DirectionalLight(0x93c5fd, 0.5)
    fill.position.set(-2, 1, 3)
    scene.add(fill)
    const rim = new THREE.DirectionalLight(0xf59e0b, 0.35)
    rim.position.set(0, 2, -3)
    scene.add(rim)

    // Floor shadow catcher
    const shadowPlane = new THREE.Mesh(
      new THREE.PlaneGeometry(4, 4),
      new THREE.ShadowMaterial({ opacity: 0.35 }),
    )
    shadowPlane.rotation.x = -Math.PI / 2
    shadowPlane.receiveShadow = true
    scene.add(shadowPlane)

    let model: THREE.Object3D | null = null
    let headMesh: THREE.SkinnedMesh | null = null
    let morphDict: Record<string, number> = {}
    const syncMeshes: THREE.SkinnedMesh[] = []
    let headBone: THREE.Bone | null = null
    let chestBone: THREE.Bone | null = null
    let spine1Bone: THREE.Bone | null = null
    let leftShoulder: THREE.Bone | null = null
    let rightShoulder: THREE.Bone | null = null
    let leftArm: THREE.Bone | null = null
    let rightArm: THREE.Bone | null = null
    let leftForeArm: THREE.Bone | null = null
    let rightForeArm: THREE.Bone | null = null
    const handBones = new Map<string, THREE.Bone>()
    const handBaselines = new Map<string, { x: number; y: number; z: number }>()

    const restRotations: Record<string, { x?: number; y?: number; z?: number }> = {
      LeftArm:               { x: 1.25, z: 0.15 },
      RightArm:              { x: 1.25, z: -0.15 },
      LeftForeArm:           { x: 0.2 },
      RightForeArm:          { x: 0.2 },
      mixamorigLeftArm:      { x: 1.25, z: 0.15 },
      mixamorigRightArm:     { x: 1.25, z: -0.15 },
      mixamorigLeftForeArm:  { x: 0.2 },
      mixamorigRightForeArm: { x: 0.2 },
    }

    const loader = new GLTFLoader()
    loader.load('/models/avaturn.glb', (gltf) => {
      model = gltf.scene

      // Single unified traversal: shadow setup, head mesh capture + rest pose
      model.traverse((child) => {
        const m = child as THREE.Mesh
        if (m.isMesh) {
          m.castShadow = true
          m.receiveShadow = true
        }

        const skinned = child as THREE.SkinnedMesh
        if (skinned.isSkinnedMesh) {
          const isHead = skinned.name === 'Head_Mesh' || skinned.name === 'Wolf3D_Head'
          if (isHead && skinned.morphTargetDictionary && skinned.morphTargetInfluences) {
            headMesh = skinned
            morphDict = skinned.morphTargetDictionary
          } else if (skinned.morphTargetDictionary && skinned.morphTargetInfluences) {
            syncMeshes.push(skinned)
          }
        }

        const b = child as THREE.Bone
        if (b.isBone) {
          const n = b.name
          if (n === 'Head') headBone = b
          else if (n === 'Spine2' || n === 'Chest' || n === 'UpperChest') chestBone = b
          else if (n === 'Spine1') spine1Bone = b
          else if (n === 'LeftShoulder') leftShoulder = b
          else if (n === 'RightShoulder') rightShoulder = b
          else if (n === 'LeftArm') leftArm = b
          else if (n === 'RightArm') rightArm = b
          else if (n === 'LeftForeArm') leftForeArm = b
          else if (n === 'RightForeArm') rightForeArm = b
          const r = restRotations[n]
          if (r) {
            if (r.x !== undefined) b.rotation.x = r.x
            if (r.y !== undefined) b.rotation.y = r.y
            if (r.z !== undefined) b.rotation.z = r.z
          }
          // Snapshot final rotation (bind-pose Y/Z + our X override) as baseline for gestures.
          // Runs regardless of whether a rest entry exists, so shoulders are captured too.
          switch (n) {
            case 'LeftArm':       armBaselines.leftArm       = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'RightArm':      armBaselines.rightArm      = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'LeftForeArm':   armBaselines.leftForeArm   = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'RightForeArm':  armBaselines.rightForeArm  = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'LeftShoulder':  armBaselines.leftShoulder  = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'RightShoulder': armBaselines.rightShoulder = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'Spine1':       bodyBaselines.spine1      = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
            case 'Spine2':
            case 'Chest':
            case 'UpperChest':   bodyBaselines.chest       = { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z }; break
          }
          if (n.startsWith('LeftHand') || n.startsWith('RightHand')) {
            handBones.set(n, b)
            handBaselines.set(n, { x: b.rotation.x, y: b.rotation.y, z: b.rotation.z })
          }
        }
      })

      scene.add(model)
    }, undefined, (err) => console.error('GLB load failed:', err))

    const VISEMES = [
      'viseme_sil', 'viseme_PP', 'viseme_FF', 'viseme_TH',
      'viseme_DD', 'viseme_kk', 'viseme_CH', 'viseme_SS',
      'viseme_nn', 'viseme_RR', 'viseme_aa', 'viseme_E',
      'viseme_I', 'viseme_O', 'viseme_U',
    ]
    const visemeWeights = new Float32Array(VISEMES.length)

    function setMorph(val: number, ...names: string[]) {
      if (!headMesh?.morphTargetInfluences) return
      for (const name of names) {
        const idx = morphDict[name]
        if (idx !== undefined) headMesh.morphTargetInfluences[idx] = val
      }
    }

    function syncAll() {
      if (!headMesh?.morphTargetInfluences) return
      for (const mesh of syncMeshes) {
        if (!mesh.morphTargetInfluences || !mesh.morphTargetDictionary) continue
        for (const [name, idx] of Object.entries(mesh.morphTargetDictionary)) {
          const headIdx = morphDict[name]
          if (headIdx !== undefined) mesh.morphTargetInfluences[idx] = headMesh.morphTargetInfluences[headIdx]
        }
      }
    }

    let gazeTargetX = 0, gazeTargetY = 0
    let gazeCurrentX = 0, gazeCurrentY = 0
    let nextGazeShift = 0
    let nextBlinkAt = Math.random() * 3 + 2
    let blinkPhase = 0 // 0 = open, 1 = closed
    let blinkTween = 0
    let tiltCurrent = 0, tiltTarget = 0, nextTiltCheck = 0
    let nextSmileAt = Math.random() * 20 + 15
    let smilePhase = 0
    let nodPhase = 0
    let lastAmpEdge = 0
    let prevAmp = 0
    let processingPose = 0  // 0 = not processing, lerps toward 1 when processing

    const armBaselines = {
      leftArm:      { x: 0, y: 0, z: 0 },
      rightArm:     { x: 0, y: 0, z: 0 },
      leftForeArm:  { x: 0, y: 0, z: 0 },
      rightForeArm: { x: 0, y: 0, z: 0 },
      leftShoulder:  { x: 0, y: 0, z: 0 },
      rightShoulder: { x: 0, y: 0, z: 0 },
    }

    const bodyBaselines = {
      spine1: { x: 0, y: 0, z: 0 },
      chest:  { x: 0, y: 0, z: 0 },
    }

    type GestureOffset = { sx?: number; sy?: number; sz?: number; ax?: number; ay?: number; az?: number; fx?: number; fy?: number; fz?: number }
    type Gesture = { left: GestureOffset; right: GestureOffset; hold: number }

    const GESTURES: Gesture[] = [
      { left: {}, right: {}, hold: 1.2 },
      { left: { sz: 0.04, ax: 0.06, az: -0.06, fy: -0.1 }, right: { sz: -0.04, ax: 0.06, az: 0.06, fy: 0.1 }, hold: 1.4 },
      { left: { ax: 0.15, az: -0.1, fy: -0.25 }, right: {}, hold: 1.6 },
      { left: { ax: 0.08, az: -0.04, fy: -0.15 }, right: { ax: 0.08, az: 0.04, fy: 0.15 }, hold: 1.5 },
    ]

    let currentGesture = 0
    let gestureEndTime = 0
    let nextGestureCheck = 0
    const armOffsets: { l: GestureOffset; r: GestureOffset } = { l: {}, r: {} }
    const armCurrent: { l: GestureOffset; r: GestureOffset } = { l: {}, r: {} }

    let raf = 0
    let time = 0
    function tick() {
      raf = requestAnimationFrame(tick)
      time += 0.016
      const s = stateRef.current
      const an = analyserRef.current

      // Processing pose: lerp toward 1 when thinking, toward 0 otherwise
      const processingTarget = stateRef.current === 'processing' ? 1 : 0
      processingPose += (processingTarget - processingPose) * 0.05

      // Subtle torso sway — gentle twist on Spine1 only. No pelvis/leg rotation
      // (procedural weight shift on a rigid skeleton without IK looks unnatural).
      if (spine1Bone) {
        spine1Bone.rotation.x = bodyBaselines.spine1.x
        spine1Bone.rotation.y = bodyBaselines.spine1.y + Math.sin(time * 0.17) * 0.01
        spine1Bone.rotation.z = bodyBaselines.spine1.z + Math.sin(time * 0.13 + 1.5) * 0.008
      }

      // Head micro-sway (always) + chest breath
      if (headBone) {
        let nodY = 0
        if (nodPhase > 0) {
          const dt = time - nodPhase
          if (dt < 0.15) nodY = -0.05 * (dt / 0.15)
          else if (dt < 0.3) nodY = -0.05 * (1 - (dt - 0.15) / 0.15)
          else nodPhase = 0
        }
        headBone.rotation.y = Math.sin(time * 0.3) * 0.015 + processingPose * 0.08
        headBone.rotation.x = Math.sin(time * 0.2) * 0.008 + tiltCurrent * 0.4 + nodY + processingPose * 0.12
        headBone.rotation.z = tiltCurrent * 0.6 - processingPose * 0.04
      }
      if (chestBone) {
        const breathPhase = Math.sin(time * (2 * Math.PI / 4))
        chestBone.rotation.x = bodyBaselines.chest.x + breathPhase * 0.02
        chestBone.rotation.y = bodyBaselines.chest.y
        chestBone.rotation.z = bodyBaselines.chest.z
      }

      // Blinks
      if (headMesh?.morphTargetInfluences) {
        if (time >= nextBlinkAt && blinkPhase === 0) {
          blinkPhase = 1
          blinkTween = 0
        }
        if (blinkPhase === 1) {
          blinkTween += 0.12
          const val = blinkTween < 1 ? blinkTween : 2 - blinkTween
          setMorph(Math.max(0, Math.min(1, val)), 'eyeBlinkLeft', 'eyeBlinkRight')
          if (blinkTween >= 2) {
            blinkPhase = 0
            setMorph(0, 'eyeBlinkLeft', 'eyeBlinkRight')
            nextBlinkAt = time + 2 + Math.random() * 3
          }
        }

        // Gaze
        if (time > nextGazeShift) {
          const shouldRoam = Math.random() < 0.3
          gazeTargetX = shouldRoam ? (Math.random() - 0.5) * 0.6 : 0
          gazeTargetY = shouldRoam ? (Math.random() - 0.5) * 0.4 : 0
          nextGazeShift = time + 2 + Math.random() * 2
        }
        gazeCurrentX += (gazeTargetX - gazeCurrentX) * 0.04
        gazeCurrentY += (gazeTargetY - gazeCurrentY) * 0.04
        const sx = (Math.sin(time * 7.3) + Math.sin(time * 11.1)) * 0.02
        const sy = (Math.sin(time * 8.9) + Math.sin(time * 13.7)) * 0.02
        const gx = gazeCurrentX + sx, gy = gazeCurrentY + sy
        setMorph(Math.max(0, gx), 'eyeLookInLeft', 'eyeLookOutRight')
        setMorph(Math.max(0, -gx), 'eyeLookOutLeft', 'eyeLookInRight')
        setMorph(Math.max(0, gy), 'eyeLookUpLeft', 'eyeLookUpRight')
        setMorph(Math.max(0, -gy), 'eyeLookDownLeft', 'eyeLookDownRight')

        // Head tilt (occasional)
        if (time > nextTiltCheck) {
          if (Math.random() < 0.08) {
            tiltTarget = (Math.random() - 0.5) * 0.16
            nextTiltCheck = time + 1 + Math.random()
          } else {
            tiltTarget = 0
            nextTiltCheck = time + 0.5
          }
        }
        tiltCurrent += (tiltTarget - tiltCurrent) * 0.03

        // Occasional soft smile
        if (time >= nextSmileAt && smilePhase === 0) {
          smilePhase = time
        }
        if (smilePhase > 0) {
          const dt = time - smilePhase
          if (dt < 1) setMorph(0.15 * dt, 'mouthSmile')
          else if (dt < 2) setMorph(0.15, 'mouthSmile')
          else if (dt < 3) setMorph(0.15 * (3 - dt), 'mouthSmile')
          else {
            setMorph(0, 'mouthSmile')
            smilePhase = 0
            nextSmileAt = time + 15 + Math.random() * 15
          }
        }
      }

      if (headMesh?.morphTargetInfluences) {
        const target = new Float32Array(VISEMES.length)
        if (an && s === 'speaking') {
          const freqData = new Float32Array(an.frequencyBinCount)
          an.getFloatFrequencyData(freqData)
          const binCount = freqData.length
          const band1End = Math.floor(binCount * 0.08)
          const band2End = Math.floor(binCount * 0.25)
          const band3End = Math.floor(binCount * 0.5)
          let low = 0, mid = 0, high = 0, total = 0
          for (let i = 1; i < band3End; i++) {
            const e = Math.pow(10, (freqData[i] + 100) / 40)
            if (i < band1End) low += e
            else if (i < band2End) mid += e
            else high += e
            total += e
          }
          const timeBuf = new Float32Array(an.fftSize)
          an.getFloatTimeDomainData(timeBuf)
          let rmsSum = 0
          for (let j = 0; j < timeBuf.length; j++) rmsSum += timeBuf[j] * timeBuf[j]
          const amplitude = Math.min(Math.sqrt(rmsSum / timeBuf.length) * 6, 1.0)

          // Brow engagement driven by amplitude
          setMorph(Math.min(0.4, amplitude * 0.6), 'browInnerUp')

          // Head nod on amplitude edges (new utterance start)
          if (prevAmp < 0.1 && amplitude > 0.25 && time - lastAmpEdge > 1.5) {
            nodPhase = time
            lastAmpEdge = time
          }
          prevAmp = amplitude

          // Gesture trigger on amplitude edge
          if (time >= nextGestureCheck && amplitude > 0.4 && Math.random() < 0.25) {
            currentGesture = 1 + Math.floor(Math.random() * (GESTURES.length - 1))
            gestureEndTime = time + 0.6 + GESTURES[currentGesture].hold
            armOffsets.l = GESTURES[currentGesture].left
            armOffsets.r = GESTURES[currentGesture].right
            nextGestureCheck = gestureEndTime + 3 + Math.random() * 4
          }

          if (amplitude > 0.02) {
            const norm = total + 0.001
            const lowR = low / norm, midR = mid / norm, highR = high / norm
            target[10] = lowR * 0.8 * amplitude
            target[13] = lowR * 0.4 * amplitude
            target[11] = midR * 0.6 * amplitude
            target[12] = midR * 0.4 * amplitude
            target[4]  = midR * 0.3 * amplitude
            target[8]  = midR * 0.3 * amplitude
            target[7]  = highR * 0.6 * amplitude
            target[2]  = highR * 0.3 * amplitude
            target[3]  = highR * 0.2 * amplitude
            target[6]  = highR * 0.3 * amplitude
            target[1]  = Math.max(0, amplitude - 0.5) * midR * 0.5
            target[5]  = Math.max(0, amplitude - 0.4) * midR * 0.3
            target[14] = lowR * midR * amplitude * 0.5
            target[9]  = midR * lowR * amplitude * 0.4
          }
        }
        // Smoothed crossfade
        for (let i = 0; i < VISEMES.length; i++) {
          visemeWeights[i] += (target[i] - visemeWeights[i]) * 0.35
          setMorph(visemeWeights[i], VISEMES[i])
        }
        syncAll()

        if (stateRef.current !== 'speaking') {
          setMorph(0, 'browInnerUp')
        }
      }

      // Gesture return-to-neutral
      if (stateRef.current !== 'speaking' || time > gestureEndTime) {
        armOffsets.l = {}
        armOffsets.r = {}
      }

      // Lerp toward target offsets
      function lerpOff(cur: GestureOffset, tgt: GestureOffset, k: number): GestureOffset {
        const out: GestureOffset = {}
        const keys: (keyof GestureOffset)[] = ['sx','sy','sz','ax','ay','az','fx','fy','fz']
        for (const key of keys) {
          const c = cur[key] ?? 0
          const t = tgt[key] ?? 0
          out[key] = c + (t - c) * k
        }
        return out
      }
      armCurrent.l = lerpOff(armCurrent.l, armOffsets.l, 0.025)
      armCurrent.r = lerpOff(armCurrent.r, armOffsets.r, 0.025)

      // Apply: baseline + offset. Never overwrite with only offset, or rest pose is lost.
      // Ambient arm drift (stronger in listening, damped in speaking so gestures can dominate)
      const driftScale = stateRef.current === 'listening' ? 1 : 0.3
      const armDriftZ = Math.sin(time * 0.38) * 0.03 * driftScale
      const armDriftX = Math.sin(time * 0.24 + 1.3) * 0.018 * driftScale
      const armDriftY = Math.sin(time * 0.31 + 0.8) * 0.015 * driftScale  // arm twist

      if (leftShoulder)  leftShoulder.rotation.set(
        armBaselines.leftShoulder.x + (armCurrent.l.sx ?? 0),
        armBaselines.leftShoulder.y + (armCurrent.l.sy ?? 0),
        armBaselines.leftShoulder.z + (armCurrent.l.sz ?? 0),
      )
      if (rightShoulder) rightShoulder.rotation.set(
        armBaselines.rightShoulder.x + (armCurrent.r.sx ?? 0),
        armBaselines.rightShoulder.y + (armCurrent.r.sy ?? 0),
        armBaselines.rightShoulder.z + (armCurrent.r.sz ?? 0),
      )
      if (leftArm)       leftArm.rotation.set(
        armBaselines.leftArm.x + (armCurrent.l.ax ?? 0) + armDriftX,
        armBaselines.leftArm.y + (armCurrent.l.ay ?? 0) + armDriftY,
        armBaselines.leftArm.z + (armCurrent.l.az ?? 0) + armDriftZ,
      )
      if (rightArm)      rightArm.rotation.set(
        armBaselines.rightArm.x + (armCurrent.r.ax ?? 0) + armDriftX,
        armBaselines.rightArm.y + (armCurrent.r.ay ?? 0) - armDriftY,  // opposite phase
        armBaselines.rightArm.z + (armCurrent.r.az ?? 0) - armDriftZ,
      )
      if (leftForeArm)   leftForeArm.rotation.set(
        armBaselines.leftForeArm.x + (armCurrent.l.fx ?? 0),
        armBaselines.leftForeArm.y + (armCurrent.l.fy ?? 0),
        armBaselines.leftForeArm.z + (armCurrent.l.fz ?? 0),
      )
      if (rightForeArm)  rightForeArm.rotation.set(
        armBaselines.rightForeArm.x + (armCurrent.r.fx ?? 0),
        armBaselines.rightForeArm.y + (armCurrent.r.fy ?? 0),
        armBaselines.rightForeArm.z + (armCurrent.r.fz ?? 0),
      )

      // Hand + finger idle motion: wrist roll + breathing finger curl
      const handPhase = Math.sin(time * 0.4)
      const wristRoll = handPhase * 0.08
      // Finger curl: amplified + applied on all 3 axes since rig axis is unverified.
      // Only one axis will do actual curl; others add tiny noise. Total motion is a slow breath.
      const fingerCurl = (Math.sin(time * 0.3) + Math.sin(time * 0.47 + 0.4)) * 0.5
      const curlAmt = 0.12   // ~7° per joint — meaningful, not exaggerated

      for (const [name, bone] of handBones) {
        const baseline = handBaselines.get(name)
        if (!baseline) continue
        const isLeft = name.startsWith('Left')
        const wristSide = isLeft ? 1 : -1

        if (name === 'LeftHand' || name === 'RightHand') {
          // Wrist: gentle roll + up/down pitch
          bone.rotation.x = baseline.x + handPhase * 0.05
          bone.rotation.y = baseline.y + handPhase * 0.03 * wristSide
          bone.rotation.z = baseline.z + wristRoll * wristSide
        } else {
          // Finger joint: breathing curl on Z (primary guess) and small amount on X/Y
          // so at least one axis produces visible motion regardless of rig convention.
          bone.rotation.x = baseline.x + fingerCurl * curlAmt * 0.5
          bone.rotation.y = baseline.y
          bone.rotation.z = baseline.z + fingerCurl * curlAmt
        }
      }

      renderer.render(scene, camera)
    }
    tick()

    function onResize() {
      const nw = el.clientWidth, nh = el.clientHeight
      camera.aspect = nw / nh
      camera.updateProjectionMatrix()
      renderer.setSize(nw, nh)
    }
    window.addEventListener('resize', onResize)

    return () => {
      cancelAnimationFrame(raf)
      window.removeEventListener('resize', onResize)
      if (model) {
        model.traverse((child) => {
          const m = child as THREE.Mesh
          if (m.isMesh) {
            m.geometry?.dispose()
            if (Array.isArray(m.material)) {
              m.material.forEach(mat => mat.dispose())
            } else {
              m.material?.dispose()
            }
          }
        })
        scene.remove(model)
      }
      renderer.dispose()
      if (el.contains(renderer.domElement)) el.removeChild(renderer.domElement)
    }
  }, [])

  return <div ref={containerRef} className="absolute inset-0 z-10" />
}
