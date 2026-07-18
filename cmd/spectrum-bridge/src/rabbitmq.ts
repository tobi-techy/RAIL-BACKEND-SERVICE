import amqp from "amqp-connection-manager";
import { ChannelWrapper } from "amqp-connection-manager";
import { ConfirmChannel, ConsumeMessage } from "amqplib";
import { childLogger } from "./logger";

const log = childLogger({ module: "rabbitmq" });

export interface InboundMessage {
  platform: string;
  user_id: string;
  thread_id?: string;
  text: string;
  space_id?: string;
  msg_id?: string;

  // voice note (transcribed on the backend)
  is_voice?: boolean;
  audio_b64?: string;
  audio_mime?: string;
}

export interface ActionEvent {
  action: "confirm" | "cancel";
  poll_title: string; // the question the vote answered (for logging/correlation)
  user_id: string;
  space_id: string;
  platform: string;
}

export interface RabbitMQHandle {
  publishInbound(msg: InboundMessage): Promise<void>;
  publishAction(event: ActionEvent): Promise<void>;
  consumeOutbound(handler: (raw: string) => Promise<void>, queueName?: string): void;
  isConnected(): boolean;
  close(): Promise<void>;
}

export function createRabbitMQ(amqpUrl: string, exchange: string, outboundQueue?: string): RabbitMQHandle {
  let connected = false;
  const connection = amqp.connect([amqpUrl]);

  connection.on("connect", () => {
    connected = true;
    log.info("connected to RabbitMQ");
  });
  connection.on("disconnect", (params) => {
    connected = false;
    log.warn({ err: params.err }, "disconnected from RabbitMQ");
  });

  const pubChannel: ChannelWrapper = connection.createChannel({
    json: true,
    setup: (ch: ConfirmChannel) => {
      return ch.assertExchange(exchange, "topic", { durable: true });
    },
  });

  async function publishInbound(msg: InboundMessage): Promise<void> {
    await pubChannel.publish(exchange, "message.inbound", msg, {
      persistent: true,
      contentType: "application/json",
    });
  }

  async function publishAction(event: ActionEvent): Promise<void> {
    await pubChannel.publish(exchange, "action.postback", event, {
      persistent: true,
      contentType: "application/json",
    });
  }

  function isConnected(): boolean {
    return connected;
  }

  function consumeOutbound(handler: (raw: string) => Promise<void>, queueName?: string): void {
    const outboundQueueName = queueName || "miriam.outbound";
    const dlx = exchange + ".dlx";
    const dlq = outboundQueueName + ".dead";

    connection.createChannel({
      json: false,
      setup: (ch: ConfirmChannel) => {
        return ch
          .assertExchange(exchange, "topic", { durable: true })
          .then(() => ch.assertExchange(dlx, "fanout", { durable: true }))
          .then(() => ch.assertQueue(dlq, { durable: true }))
          .then(() => ch.bindQueue(dlq, dlx, ""))
          .then(() =>
            ch.assertQueue(outboundQueueName, {
              durable: true,
              arguments: { "x-dead-letter-exchange": dlx },
            }),
          )
          .then((ok) => ch.bindQueue(ok.queue, exchange, "message.outbound"))
          .then(() =>
            ch.consume(outboundQueueName, async (msg: ConsumeMessage | null) => {
              if (!msg) return;
              try {
                await handler(msg.content.toString("utf-8"));
                ch.ack(msg);
              } catch (err: unknown) {
                const errMsg = err instanceof Error ? err.message : String(err);
                if (err instanceof RetryableOutboundError) {
                  log.warn({ err: errMsg }, "transient outbound error, requeueing");
                  ch.nack(msg, false, true);
                } else {
                  log.error({ err: errMsg }, "permanent outbound error, dead-lettering");
                  ch.nack(msg, false, false);
                }
              }
            }),
          );
      },
    });
    log.info({ queue: outboundQueueName, dlq }, "outbound consumer configured with DLQ");
  }

  async function close(): Promise<void> {
    await pubChannel.close();
    await connection.close();
  }

  return { publishInbound, publishAction, consumeOutbound, isConnected, close };
}

/**
 * RetryableOutboundError marks a transient failure that should be requeued.
 * All other errors are treated as permanent and dead-lettered.
 */
export class RetryableOutboundError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RetryableOutboundError";
  }
}
