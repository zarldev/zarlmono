import type { TalkingHead } from '@met4citizen/talkinghead'

// Custom gesture templates that extend the built-in set from @met4citizen/talkinghead.
// Each entry is a bone-rotation dictionary in the same shape the library expects
// in `gestureTemplates`: keys are `Bone.rotation`, values are per-axis numbers or
// `[min, max, t, n]` tuples that the library animates between while the gesture
// is held.
//
// Template values are tuned against the Mixamo-style rig used by Avaturn / RPM
// exports. Other rigs (VRM, custom) may interpret rotations differently — if a
// new avatar looks off, tune per-gesture here rather than scattering adjustments.
//
// Left-hand gestures work with `mirror=true` in playGesture to flip to the right
// hand. Gestures that reference both hands (e.g. `clap`) cannot be mirrored.

const CUSTOM_GESTURES: Record<string, Record<string, unknown>> = {
  // Friendly greeting — arm raised like `handup`, hand oscillates side-to-side.
  wave: {
    'LeftShoulder.rotation': { x: [1.5, 2, 1, 2], y: [0.2, 0.4, 1, 2], z: [-1.5, -1.3, 1, 2] },
    'LeftArm.rotation': { x: [1.5, 1.7, 1, 2], y: [-0.6, -0.4, 1, 2], z: [1, 1.2, 1, 2] },
    'LeftForeArm.rotation': { x: -0.815, y: [-0.6, 0.2, 1, 1], z: [1.3, 1.8, 1, 1] },
    'LeftHand.rotation': { x: -0.3, y: [-0.5, 0.3, 1, 1], z: [0.0, 0.5, 1, 1] },
    'LeftHandThumb1.rotation': { x: 0.1, y: -0.3, z: 0.2 },
    'LeftHandThumb2.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandThumb3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandIndex1.rotation': { x: 0, y: -0.05, z: 0.1 },
    'LeftHandIndex2.rotation': { x: 0.1, y: 0, z: 0 },
    'LeftHandIndex3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandMiddle1.rotation': { x: 0, y: -0.1, z: 0 },
    'LeftHandMiddle2.rotation': { x: 0.1, y: 0, z: 0 },
    'LeftHandMiddle3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandRing1.rotation': { x: 0, y: -0.2, z: -0.1 },
    'LeftHandRing2.rotation': { x: 0.1, y: 0, z: 0 },
    'LeftHandRing3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandPinky1.rotation': { x: 0.05, y: -0.3, z: -0.2 },
    'LeftHandPinky2.rotation': { x: 0.1, y: 0, z: 0 },
    'LeftHandPinky3.rotation': { x: 0, y: 0, z: 0 },
  },

  // V-sign — index and middle fingers extended, others curled. Based on `index`
  // with the middle finger straightened.
  peace: {
    'LeftShoulder.rotation': { x: [1.5, 2, 1, 2], y: [0.2, 0.4, 1, 2], z: [-1.5, -1.3, 1, 2] },
    'LeftArm.rotation': { x: [1.5, 1.7, 1, 2], y: [-0.6, -0.4, 1, 2], z: [1, 1.2, 1, 2] },
    'LeftForeArm.rotation': { x: -0.815, y: [-0.4, 0, 1, 2], z: 1.575 },
    'LeftHand.rotation': { x: -0.276, y: -0.506, z: -0.208 },
    'LeftHandThumb1.rotation': { x: 0.579, y: 0.228, z: 0.363 },
    'LeftHandThumb2.rotation': { x: -0.027, y: -0.04, z: -0.662 },
    'LeftHandThumb3.rotation': { x: 0, y: 0.001, z: 0 },
    'LeftHandIndex1.rotation': { x: 0, y: -0.105, z: 0.225 },
    'LeftHandIndex2.rotation': { x: 0.256, y: -0.103, z: -0.213 },
    'LeftHandIndex3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandMiddle1.rotation': { x: 0, y: 0.1, z: -0.15 },
    'LeftHandMiddle2.rotation': { x: 0.25, y: 0, z: 0 },
    'LeftHandMiddle3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandRing1.rotation': { x: 1.528, y: -0.073, z: 0.052 },
    'LeftHandRing2.rotation': { x: 1.386, y: 0.044, z: 0.053 },
    'LeftHandRing3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandPinky1.rotation': { x: 1.65, y: -0.204, z: 0.031 },
    'LeftHandPinky2.rotation': { x: 1.302, y: 0.071, z: 0.085 },
    'LeftHandPinky3.rotation': { x: 0, y: 0, z: 0 },
  },

  // Flat palm facing forward — "stop / wait / hold on". Based on `handup` with
  // the hand flattened and all fingers extended.
  stop: {
    'LeftShoulder.rotation': { x: [1.6, 1.9, 1, 2], y: [0.3, 0.5, 1, 2], z: [-1.4, -1.2, 1, 2] },
    'LeftArm.rotation': { x: [1.4, 1.6, 1, 2], y: [-0.5, -0.3, 1, 2], z: [0.9, 1.1, 1, 2] },
    'LeftForeArm.rotation': { x: -0.5, y: -0.2, z: 1.575 },
    'LeftHand.rotation': { x: 0, y: -0.3, z: 0.2 },
    'LeftHandThumb1.rotation': { x: 0.2, y: -0.3, z: 0.4 },
    'LeftHandThumb2.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandThumb3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandIndex1.rotation': { x: 0, y: -0.05, z: 0.1 },
    'LeftHandIndex2.rotation': { x: 0.05, y: 0, z: 0 },
    'LeftHandIndex3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandMiddle1.rotation': { x: 0, y: -0.1, z: 0 },
    'LeftHandMiddle2.rotation': { x: 0.05, y: 0, z: 0 },
    'LeftHandMiddle3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandRing1.rotation': { x: 0, y: -0.2, z: -0.1 },
    'LeftHandRing2.rotation': { x: 0.05, y: 0, z: 0 },
    'LeftHandRing3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandPinky1.rotation': { x: 0.05, y: -0.3, z: -0.2 },
    'LeftHandPinky2.rotation': { x: 0.05, y: 0, z: 0 },
    'LeftHandPinky3.rotation': { x: 0, y: 0, z: 0 },
  },

  // Pointing at own chest — "me?" / "I". Arm folded close to body, index
  // extended toward sternum.
  pointself: {
    'LeftShoulder.rotation': { x: 1.1, y: 0.2, z: -0.3 },
    'LeftArm.rotation': { x: 1.0, y: 0.0, z: 0.2 },
    'LeftForeArm.rotation': { x: -1.8, y: -0.3, z: 1.0 },
    'LeftHand.rotation': { x: 0, y: -0.5, z: -0.3 },
    'LeftHandThumb1.rotation': { x: 0.579, y: 0.228, z: 0.363 },
    'LeftHandThumb2.rotation': { x: -0.027, y: -0.04, z: -0.662 },
    'LeftHandThumb3.rotation': { x: 0, y: 0.001, z: 0 },
    'LeftHandIndex1.rotation': { x: 0, y: -0.105, z: 0.225 },
    'LeftHandIndex2.rotation': { x: 0.256, y: -0.103, z: -0.213 },
    'LeftHandIndex3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandMiddle1.rotation': { x: 1.453, y: 0.07, z: 0.021 },
    'LeftHandMiddle2.rotation': { x: 1.599, y: 0.062, z: 0.07 },
    'LeftHandMiddle3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandRing1.rotation': { x: 1.528, y: -0.073, z: 0.052 },
    'LeftHandRing2.rotation': { x: 1.386, y: 0.044, z: 0.053 },
    'LeftHandRing3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandPinky1.rotation': { x: 1.65, y: -0.204, z: 0.031 },
    'LeftHandPinky2.rotation': { x: 1.302, y: 0.071, z: 0.085 },
    'LeftHandPinky3.rotation': { x: 0, y: 0, z: 0 },
  },

  // Fist pump — "yes! / nice!". Arm bent, hand closed, raised vertically.
  fistpump: {
    'LeftShoulder.rotation': { x: 1.6, y: 0.3, z: -1.2 },
    'LeftArm.rotation': { x: 1.5, y: -0.3, z: 0.8 },
    'LeftForeArm.rotation': { x: -2.0, y: 0.2, z: 1.575 },
    'LeftHand.rotation': { x: -0.3, y: -0.2, z: 0 },
    'LeftHandThumb1.rotation': { x: 0.5, y: -0.3, z: 0.7 },
    'LeftHandThumb2.rotation': { x: 0.3, y: -0.1, z: -0.3 },
    'LeftHandThumb3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandIndex1.rotation': { x: 1.5, y: -0.1, z: -0.1 },
    'LeftHandIndex2.rotation': { x: 1.9, y: -0.1, z: -0.1 },
    'LeftHandIndex3.rotation': { x: 0.5, y: 0, z: 0 },
    'LeftHandMiddle1.rotation': { x: 1.5, y: -0.1, z: -0.1 },
    'LeftHandMiddle2.rotation': { x: 1.9, y: -0.1, z: -0.1 },
    'LeftHandMiddle3.rotation': { x: 0.5, y: 0, z: 0 },
    'LeftHandRing1.rotation': { x: 1.6, y: -0.1, z: -0.05 },
    'LeftHandRing2.rotation': { x: 1.9, y: -0.1, z: -0.1 },
    'LeftHandRing3.rotation': { x: 0.3, y: 0, z: 0 },
    'LeftHandPinky1.rotation': { x: 1.7, y: -0.1, z: 0 },
    'LeftHandPinky2.rotation': { x: 1.7, y: -0.1, z: -0.1 },
    'LeftHandPinky3.rotation': { x: 0.6, y: 0, z: 0 },
  },

  // Beckoning — "come here". Arm at chest height, fingers curling inward.
  beckon: {
    'LeftShoulder.rotation': { x: 1.3, y: 0.2, z: -0.8 },
    'LeftArm.rotation': { x: 1.2, y: -0.5, z: 0.5 },
    'LeftForeArm.rotation': { x: -1.2, y: -0.3, z: 1.2 },
    'LeftHand.rotation': { x: -0.8, y: -0.3, z: 0.2 },
    'LeftHandThumb1.rotation': { x: 0.3, y: -0.3, z: 0.5 },
    'LeftHandThumb2.rotation': { x: 0, y: 0, z: -0.2 },
    'LeftHandThumb3.rotation': { x: 0, y: 0, z: 0 },
    'LeftHandIndex1.rotation': { x: [0.9, 1.5, 1, 1], y: -0.1, z: 0 },
    'LeftHandIndex2.rotation': { x: [0.9, 1.5, 1, 1], y: -0.1, z: 0 },
    'LeftHandIndex3.rotation': { x: 0.3, y: 0, z: 0 },
    'LeftHandMiddle1.rotation': { x: [0.9, 1.5, 1, 1], y: -0.1, z: 0 },
    'LeftHandMiddle2.rotation': { x: [0.9, 1.5, 1, 1], y: -0.1, z: 0 },
    'LeftHandMiddle3.rotation': { x: 0.3, y: 0, z: 0 },
    'LeftHandRing1.rotation': { x: [0.9, 1.5, 1, 1], y: -0.2, z: -0.1 },
    'LeftHandRing2.rotation': { x: [0.9, 1.5, 1, 1], y: -0.1, z: 0 },
    'LeftHandRing3.rotation': { x: 0.3, y: 0, z: 0 },
    'LeftHandPinky1.rotation': { x: [0.9, 1.5, 1, 1], y: -0.3, z: -0.2 },
    'LeftHandPinky2.rotation': { x: [0.9, 1.5, 1, 1], y: -0.1, z: 0 },
    'LeftHandPinky3.rotation': { x: 0.3, y: 0, z: 0 },
  },
}

export const CUSTOM_GESTURE_NAMES = Object.keys(CUSTOM_GESTURES) as ReadonlyArray<string>

// Merge our templates into the TalkingHead instance. Call once after showAvatar
// resolves. Re-calling is safe — it overwrites any prior registration, which
// matters when an avatar hot-swap recreates the rig.
export function registerCustomGestures(head: TalkingHead): void {
  for (const [name, template] of Object.entries(CUSTOM_GESTURES)) {
    head.gestureTemplates[name] = template
  }
}
