import pino from "pino";

let rootLogger: pino.Logger;

export function getLogger(): pino.Logger {
  if (!rootLogger) {
    rootLogger = pino({
      level: process.env.LOG_LEVEL || "info",
      transport:
        process.env.NODE_ENV === "development"
          ? { target: "pino-pretty", options: { colorize: true } }
          : undefined,
      formatters: {
        level(label) {
          return { level: label };
        },
      },
      serializers: {
        err: pino.stdSerializers.err,
      },
    });
  }
  return rootLogger;
}

export function childLogger(bindings: Record<string, unknown>): pino.Logger {
  return getLogger().child(bindings);
}
