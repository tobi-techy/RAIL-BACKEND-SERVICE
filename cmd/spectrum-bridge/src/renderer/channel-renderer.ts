export type RenderStrategy =
  | "text"
  | "cards"
  | "plan"
  | "trace"
  | "poll"
  | "quick_replies"
  | "voice";

export interface ActionChip {
  label: string;
  action: string;
  confirm?: boolean;
}

export interface PlanData {
  plan_id: string;
  steps: Array<{
    id: number;
    tool: string;
    status: string;
    check?: string;
  }>;
  status: string;
}

export interface TraceData {
  trace_id: string;
  content: Record<string, unknown>;
}

export interface RenderRequest {
  strategy: RenderStrategy;
  text: string;
  platform: string;
  channel?: {
    supportsPolls?: boolean;
    supportsEffects?: boolean;
    supportsQuickReplies?: boolean;
    supportsInlineActions?: boolean;
    supportsRichCards?: boolean;
    maxBubblesPerReply?: number;
    maxCharsPerBubble?: number;
  };
  actionChips?: ActionChip[];
  planData?: PlanData;
  traceData?: TraceData;
  pollOptions?: string[];
}

export interface RenderedBubble {
  text?: string;
  contentType?:
    | "text"
    | "markdown"
    | "cards"
    | "poll"
    | "quick_replies"
    | "trace"
    | "voice";
  pollTitle?: string;
  pollOptions?: string[];
  actionChips?: ActionChip[];
  planData?: PlanData;
  traceData?: TraceData;
  effect?: string;
}

const MAX_BUBBLES_BY_PLATFORM: Record<string, number> = {
  imessage: 8,
  whatsapp: 3,
  telegram: 5,
  sms: 1,
  terminal: 10,
};

const MAX_CHARS_BY_PLATFORM: Record<string, number> = {
  imessage: 2000,
  whatsapp: 4096,
  telegram: 4096,
  sms: 1600,
  terminal: 4096,
};

export function renderMessage(req: RenderRequest): RenderedBubble[] {
  const platform = req.platform.toLowerCase();
  const maxBubbles = req.channel?.maxBubblesPerReply ?? MAX_BUBBLES_BY_PLATFORM[platform] ?? 1;
  const maxChars = req.channel?.maxCharsPerBubble ?? MAX_CHARS_BY_PLATFORM[platform] ?? 1000;

  const bubbles: RenderedBubble[] = [];

  switch (req.strategy) {
    case "plan":
      return renderPlan(req, maxBubbles, maxChars);
    case "trace":
      return renderTrace(req, maxBubbles, maxChars);
    case "poll":
      return renderPoll(req, maxBubbles, maxChars);
    case "quick_replies":
      return renderQuickReplies(req, maxBubbles, maxChars);
    case "cards":
      return renderCards(req, maxBubbles, maxChars);
    case "voice":
      return renderVoice(req, maxBubbles, maxChars);
    case "text":
    default:
      return renderText(req, maxBubbles, maxChars);
  }
}

function splitBubbles(text: string, maxBubbles: number, maxChars: number): string[] {
  const paragraphs = text.split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean);
  const bubbles: string[] = [];
  let current = "";

  for (const paragraph of paragraphs) {
    if (!current) {
      current = paragraph;
      continue;
    }

    if (
      bubbles.length >= maxBubbles ||
      current.length + 1 + paragraph.length > maxChars
    ) {
      bubbles.push(current);
      current = paragraph;
    } else {
      current += `\n\n${paragraph}`;
    }
  }

  if (current) {
    bubbles.push(current);
  }

  if (bubbles.length === 0) {
    return [text.trim()];
  }

  return bubbles.slice(0, maxBubbles);
}

function renderText(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const bubbles = splitBubbles(req.text || "", maxBubbles, maxChars);
  return bubbles.map((text) => ({
    text,
    contentType: "markdown",
  }));
}

function renderCards(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const bubbles = renderText(req, maxBubbles, maxChars);
  if (bubbles.length > 0 && req.actionChips?.length) {
    bubbles[bubbles.length - 1].actionChips = req.actionChips;
  }
  if (bubbles.length > 0) {
    bubbles[bubbles.length - 1].contentType = "cards";
  }
  return bubbles;
}

function renderPlan(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const plan = req.planData;
  if (!plan) {
    return renderText(req, maxBubbles, maxChars);
  }

  const intro = req.text?.trim() ? `${req.text.trim()}\n\n` : "";
  const lines = plan.steps.map(
    (step, index) => `${index + 1}. ${formatPlanStep(step)}`
  );
  const bulletText = `${intro}${lines.join("\n")}`;
  const bubbles = splitBubbles(bulletText, maxBubbles, maxChars);

  const result: RenderedBubble[] = bubbles.map((text) => ({
    text,
    contentType: "markdown",
  }));

  if (result.length > 0) {
    result[result.length - 1].planData = plan;
    result[result.length - 1].actionChips = req.actionChips;
  }

  return result;
}

function formatPlanStep(step: { tool: string; status: string; check?: string }): string {
  const statusEmojiMap: Record<string, string> = {
    pending: "⬜",
    running: "🔵",
    done: "✅",
    failed: "❌",
  };
  const statusEmoji = statusEmojiMap[step.status] || "•";
  let line = `${statusEmoji} ${humanizeTool(step.tool)}`;
  if (step.check) {
    line += ` — ${step.check}`;
  }
  return line;
}

function humanizeTool(tool: string): string {
  return tool
    .replace(/_/g, " ")
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

function renderTrace(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const trace = req.traceData;
  if (!trace) {
    return renderText(req, maxBubbles, maxChars);
  }

  const intro = req.text?.trim() ? `${req.text.trim()}\n\n` : "";
  const summary = buildTraceSummary(trace.content);
  const fullText = `${intro}${summary}`;
  const bubbles = splitBubbles(fullText, maxBubbles, maxChars);

  const result: RenderedBubble[] = bubbles.map((text) => ({
    text,
    contentType: "markdown",
  }));

  if (result.length > 0) {
    result[result.length - 1].traceData = trace;
  }

  return result;
}

function buildTraceSummary(content: Record<string, unknown>): string {
  const lines: string[] = [];
  if (content.balance_check) lines.push(`• Balance check: ${content.balance_check}`);
  if (content.budget_remaining) lines.push(`• Budget remaining: ${content.budget_remaining}`);
  if (content.anomaly_check) lines.push(`• Anomaly check: ${content.anomaly_check}`);
  if (content.confidence) lines.push(`• Confidence: ${content.confidence}`);
  if (content.model_version) lines.push(`• Model: ${content.model_version}`);
  return lines.join("\n") || "No reasoning trace available.";
}

function renderPoll(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const options = req.pollOptions?.length ? req.pollOptions : ["Confirm", "Cancel"];
  const supportsPolls = req.channel?.supportsPolls ?? req.platform === "imessage";

  if (!supportsPolls) {
    const prompt = req.text?.trim()
      ? `${req.text.trim()}\n\nReply YES to confirm or NO to cancel.`
      : "Reply YES to confirm or NO to cancel.";
    const bubbles = splitBubbles(prompt, maxBubbles, maxChars);
    return bubbles.map((text) => ({
      text,
      contentType: "text",
    }));
  }

  const text = splitBubbles(req.text || "", maxBubbles, maxChars).join("\n\n");
  return [
    {
      text,
      contentType: "poll",
      pollTitle: text || "Confirm?",
      pollOptions: options,
    },
  ];
}

function renderQuickReplies(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const text = splitBubbles(req.text || "", maxBubbles, maxChars).join("\n\n");
  return [
    {
      text,
      contentType: "markdown",
      actionChips: req.actionChips,
    },
  ];
}

function renderVoice(req: RenderRequest, maxBubbles: number, maxChars: number): RenderedBubble[] {
  const text = splitBubbles(req.text || "", maxBubbles, maxChars).join("\n\n");
  return [
    {
      text,
      contentType: "voice",
    },
  ];
}

export function channelHintToRenderStrategy(hint?: RenderStrategy): RenderStrategy {
  if (hint && ["text", "cards", "plan", "trace", "poll", "quick_replies", "voice"].includes(hint)) {
    return hint;
  }
  return "text";
}
