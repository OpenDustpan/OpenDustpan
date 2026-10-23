// This file is the hand-written adapter of the TypeScript client for the client
// conformance suite. It speaks JSON-RPC 2.0 on standard input and output, one
// message on each line, and calls the client through its operation table, so it
// has no code for a single operation.
import { createInterface } from "node:readline";

import { ApiError, createFetchTransport, execute, metadata, operations } from "./dist/index.js";

const protocolVersion = 1;
const clientName = "@opendustpan/client";
const maxLineBytes = 1 << 20;

const codeParse = -32700;
const codeMethodNotFound = -32601;
const codeInvalidParams = -32602;
const codeServer = -32000;

let transport;

class RpcError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

function send(message) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", ...message })}\n`);
}

function initialize(params) {
  if (params?.protocolVersion !== protocolVersion) {
    throw new RpcError(codeInvalidParams, "initialize needs protocol version 1");
  }
  try {
    transport = createFetchTransport({ baseUrl: params.baseURL, token: params.token });
  } catch (error) {
    throw new RpcError(codeInvalidParams, error.message);
  }
  return {
    protocolVersion,
    client: clientName,
    clientVersion: metadata.clientVersion,
    descriptionVersion: metadata.descriptionVersion,
    serverRelease: metadata.serverRelease,
    transports: ["fetch"],
  };
}

function errorResult(error) {
  switch (error.kind) {
    case "server":
      return { code: error.status, message: error.message, errorCode: error.errorCode, errorArgs: error.errorArgs };
    case "invalid":
      return { code: error.status, message: error.message, invalid: true };
    default:
      return { code: 0, message: error.message, transport: true };
  }
}

async function call(params) {
  if (transport === undefined) {
    throw new RpcError(codeServer, "initialize comes first");
  }
  if (typeof params?.operation !== "string" || !Object.hasOwn(operations, params.operation)) {
    throw new RpcError(codeInvalidParams, `unknown operation ${JSON.stringify(params?.operation)}`);
  }
  const input = {
    path: params.path,
    query: params.query,
    body: params.body,
    ifMatch: params.ifMatch || undefined,
    idempotencyKey: params.idempotencyKey || undefined,
  };
  try {
    const answer = await execute(transport, operations[params.operation], input);
    return { status: answer.status, etag: answer.etag, body: answer.body };
  } catch (error) {
    if (!(error instanceof ApiError)) {
      throw error;
    }
    if (error.kind === "request") {
      throw new RpcError(codeInvalidParams, error.message);
    }
    return { status: error.status, error: errorResult(error) };
  }
}

async function handle(message) {
  switch (message.method) {
    case "initialize":
      return { result: initialize(message.params) };
    case "call":
      return { result: await call(message.params) };
    case "paginate":
      throw new RpcError(codeServer, "this adapter does not paginate yet");
    case "shutdown":
      return { result: null, stop: true };
    default:
      throw new RpcError(codeMethodNotFound, "unknown method");
  }
}

const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
let queue = Promise.resolve();

lines.on("line", (line) => {
  queue = queue.then(async () => {
    if (Buffer.byteLength(line) > maxLineBytes) {
      send({ id: null, error: { code: codeParse, message: "the line is too long" } });
      return;
    }
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      send({ id: null, error: { code: codeParse, message: "the line is not JSON" } });
      return;
    }
    try {
      const { result, stop } = await handle(message);
      send({ id: message.id, result });
      if (stop) {
        // The reply is out before the process exits.
        process.stdout.write("", () => process.exit(0));
      }
    } catch (error) {
      const code = error instanceof RpcError ? error.code : codeServer;
      send({ id: message.id, error: { code, message: String(error.message ?? error) } });
    }
  });
});
