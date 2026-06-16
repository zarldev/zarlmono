import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { create } from '@bufbuild/protobuf'
import { ZarlService, ConverseRequestSchema, SubscribeNotificationsRequestSchema } from '@/gen/zarl/v1/zarl_pb'
import type { ConverseResponse } from '@/gen/zarl/v1/zarl_pb'

const transport = createConnectTransport({
  baseUrl: window.location.origin,
})

const client = createClient(ZarlService, transport)

export interface ConverseCallbacks {
  onSessionCreated: (sessionId: string) => void
  onText: (text: string, durationSec: number) => void
  // Called for each streamed content fragment (streaming-capable LLMs
  // only). Consumers usually accumulate these into the live-reply display;
  // the final aggregate still arrives as onText when the turn completes.
  onTextChunk?: (text: string) => void
  // Called for each streamed reasoning fragment from a thinking-mode LLM.
  // Never spoken by TTS, never persisted in session history — purely a UI
  // affordance so long turns feel productive.
  onReasoningChunk?: (text: string) => void
  onTranscription?: (text: string, durationSec: number) => void
  onAudioStart?: (sampleRate: number, sentenceCount: number) => void
  onAudioChunk?: (pcm: Uint8Array, index: number) => void
  onAudioEnd?: (durationSec: number) => void
  onError?: (err: Error) => void
  onToolStatus?: (toolName: string, status: string, summary: string) => void
}

export interface LocationInfo {
  latitude: number
  longitude: number
}

function handleResponse(response: ConverseResponse, callbacks: ConverseCallbacks) {
  const p = response.payload
  if (!p) return

  switch (p.case) {
    case 'sessionCreated':
      callbacks.onSessionCreated(p.value.sessionId)
      break
    case 'text':
      callbacks.onText(p.value.text, p.value.durationSec)
      break
    case 'transcription':
      callbacks.onTranscription?.(p.value.text, p.value.durationSec)
      break
    case 'audioStart':
      callbacks.onAudioStart?.(p.value.sampleRate, p.value.sentenceCount)
      break
    case 'audioChunk':
      callbacks.onAudioChunk?.(p.value.pcm, p.value.index)
      break
    case 'audioEnd':
      callbacks.onAudioEnd?.(p.value.durationSec)
      break
    case 'toolStatus':
      callbacks.onToolStatus?.(p.value.toolName, p.value.status, p.value.summary)
      break
    case 'textChunk':
      callbacks.onTextChunk?.(p.value.text)
      break
    case 'reasoningChunk':
      callbacks.onReasoningChunk?.(p.value.text)
      break
  }
}

export async function sendText(
  sessionId: string,
  text: string,
  callbacks: ConverseCallbacks,
  signal?: AbortSignal,
  location?: LocationInfo,
) {
  const req = create(ConverseRequestSchema, {
    sessionId,
    input: {
      case: 'textInput',
      value: { text, imageJpeg: new Uint8Array() },
    },
    latitude: location?.latitude ?? 0,
    longitude: location?.longitude ?? 0,
  })

  try {
    for await (const response of client.converse(req, { signal })) {
      handleResponse(response, callbacks)
    }
  } catch (err) {
    if (err instanceof Error && err.name === 'AbortError') return
    callbacks.onError?.(err instanceof Error ? err : new Error(String(err)))
  }
}

export async function sendAudio(
  sessionId: string,
  wav: Uint8Array,
  imageJpeg: Uint8Array | undefined,
  callbacks: ConverseCallbacks,
  signal?: AbortSignal,
  location?: LocationInfo,
) {
  const req = create(ConverseRequestSchema, {
    sessionId,
    input: {
      case: 'audioInput',
      value: { wav, imageJpeg: imageJpeg ?? new Uint8Array() },
    },
    latitude: location?.latitude ?? 0,
    longitude: location?.longitude ?? 0,
  })

  try {
    for await (const response of client.converse(req, { signal })) {
      handleResponse(response, callbacks)
    }
  } catch (err) {
    if (err instanceof Error && err.name === 'AbortError') return
    callbacks.onError?.(err instanceof Error ? err : new Error(String(err)))
  }
}

export async function subscribeNotifications(
  sessionId: string,
  onNotification: (toolName: string, content: string) => void,
  signal?: AbortSignal,
) {
  const req = create(SubscribeNotificationsRequestSchema, { sessionId })
  try {
    for await (const msg of client.subscribeNotifications(req, { signal })) {
      onNotification(msg.toolName, msg.content)
    }
  } catch (err) {
    if (err instanceof Error && err.name === 'AbortError') return
    console.error('notification subscription error:', err)
  }
}
