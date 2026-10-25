import type { Transport, Snapshot } from "./types";

async function nativeService() {
  if (!("_wails" in window))
    throw new Error("Open the native workbench to connect");
  return import("./bindings/github.com/zarldev/zarlmono/zarlcode/desktop/session/service");
}

// Browser preview is disconnected. Fixtures exist only inside tests, never as
// an implicit fallback for a failed native connection.
export const transport: Transport = {
  async connect(options) {
    const service = await nativeService();
    return (await service.Connect(options)) as Snapshot;
  },
  async command(command) {
    const service = await nativeService();
    return (await service.Command({
      action: command.action,
      text: command.text ?? "",
      provider: command.provider ?? "",
      model: command.model ?? "",
      since: command.since ?? "",
    })) as Snapshot;
  },
};
