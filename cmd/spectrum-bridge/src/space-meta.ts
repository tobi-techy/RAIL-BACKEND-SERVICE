import type { Message, Space } from "spectrum-ts";
import { imessage } from "spectrum-ts/providers/imessage";
import { childLogger } from "./logger";

const log = childLogger({ module: "space-meta" });

export interface SpaceMeta {
  platform: string;
  /** iMessage line (phone) owning the conversation, when known. Required by
   *  `im.space.get(id, {phone})` once the project has 2+ dedicated lines. */
  phone?: string;
  /** iMessage `dm` | `group`, when known. */
  spaceType?: string;
  /** Sender reachability service (iMessage/SMS/RCS) when the provider exposes it. */
  senderService?: string;
}

/**
 * Narrow a generic Space/Message into its iMessage shape to recover `phone`,
 * `type`, and sender `service`. Gated on platform — narrowing a space from the
 * wrong platform logs a runtime warning inside the SDK, so non-iMessage
 * platforms return `{platform}` only.
 */
export function extractSpaceMeta(space: Space, platform: string, message?: Message): SpaceMeta {
  if (platform !== "imessage") return { platform };
  try {
    const im = imessage(space) as unknown as { phone?: string; type?: string };
    let senderService: string | undefined;
    if (message && message.platform === "imessage") {
      try {
        const narrowed = imessage(message) as unknown as {
          sender?: { service?: string };
        };
        senderService = narrowed?.sender?.service;
      } catch {
        // sender extras are best-effort; phone/type above is what matters.
      }
    }
    return {
      platform,
      phone: typeof im?.phone === "string" ? im.phone : undefined,
      spaceType: typeof im?.type === "string" ? im.type : undefined,
      senderService,
    };
  } catch (err) {
    log.debug({ err }, "imessage narrow failed, continuing without phone/type");
    return { platform };
  }
}
