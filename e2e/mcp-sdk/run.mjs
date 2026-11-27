#!/usr/bin/env node
/**
 * Official @modelcontextprotocol/sdk StreamableHTTP client regression.
 *
 * CallToolResultSchema (Zod) rejects structuredContent that is not a JSON
 * object — the exact failure mode from issue #37. Env:
 *   BASE_URL  – control-plane origin (e.g. http://127.0.0.1:8080)
 *   PAT       – dj_pat_... bearer token
 */
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { CallToolResultSchema } from "@modelcontextprotocol/sdk/types.js";
import { randomUUID } from "node:crypto";

const BASE_URL = (process.env.BASE_URL || "").replace(/\/$/, "");
const PAT = process.env.PAT || "";

function fail(msg) {
  console.error(`FAIL: ${msg}`);
  process.exit(1);
}

function assertObject(label, value) {
  if (value === undefined || value === null) {
    fail(`${label}: structuredContent is required on successful tool calls`);
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    fail(`${label}: structuredContent must be a JSON object, got ${Array.isArray(value) ? "array" : typeof value}`);
  }
}

function assertArrayField(obj, field, minLen = 0) {
  const v = obj?.[field];
  if (!Array.isArray(v)) {
    fail(`expected ${field} to be an array (never null), got ${JSON.stringify(v)}`);
  }
  if (v.length < minLen) {
    fail(`expected ${field}.length >= ${minLen}, got ${v.length}`);
  }
  return v;
}

async function httpJSON(method, path, body, bearer) {
  const headers = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (bearer) headers.Authorization = `Bearer ${bearer}`;
  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let json = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = text;
  }
  return { status: res.status, json, text };
}

async function callTool(client, name, args = {}) {
  // client.request + CallToolResultSchema is the strict Zod path used by hosts.
  const result = await client.request(
    { method: "tools/call", params: { name, arguments: args } },
    CallToolResultSchema,
  );
  if (result.isError) {
    fail(`${name} returned isError unexpectedly: ${JSON.stringify(result)}`);
  }
  assertObject(`${name}.structuredContent`, result.structuredContent);
  return result;
}

function structuredPayload(result) {
  assertObject("structuredContent", result.structuredContent);
  return result.structuredContent;
}

async function main() {
  if (!BASE_URL) fail("BASE_URL is required");
  if (!PAT.startsWith("dj_pat_")) fail("PAT must be a dj_pat_ token");

  const transport = new StreamableHTTPClientTransport(new URL(`${BASE_URL}/mcp`), {
    requestInit: {
      headers: { Authorization: `Bearer ${PAT}` },
    },
  });
  const client = new Client({ name: "dad-joke-mcp-sdk-e2e", version: "0.0.1" });
  await client.connect(transport);

  try {
    const tools = await client.listTools();
    const names = new Set((tools.tools || []).map((t) => t.name));
    const need = [
      "list_machines",
      "get_machine",
      "set_policy",
      "run_script",
      "get_job",
      "list_events",
      "manage_agent_tokens",
    ];
    for (const n of need) {
      if (!names.has(n)) fail(`tools/list missing ${n}`);
    }
    console.log("listTools ok:", [...names].sort().join(","));

    // Empty list_machines before enrollment.
    let res = await callTool(client, "list_machines", {});
    let payload = structuredPayload(res);
    assertArrayField(payload, "machines", 0);
    if (payload.machines.length !== 0) fail("list_machines empty: expected []");
    console.log("list_machines empty ok");

    // Empty manage_agent_tokens list.
    res = await callTool(client, "manage_agent_tokens", { action: "list" });
    payload = structuredPayload(res);
    assertArrayField(payload, "tokens", 0);
    if (payload.tokens.length !== 0) fail("manage_agent_tokens list empty: expected []");
    console.log("manage_agent_tokens list empty ok");

    // create → list → enroll → get_machine → set_policy → run_script → get_job → list_events → revoke
    res = await callTool(client, "manage_agent_tokens", {
      action: "create",
      name: `sdk-e2e-${randomUUID().slice(0, 8)}`,
    });
    payload = structuredPayload(res);
    const agentTok = payload.token;
    const tokID = payload.id;
    if (typeof agentTok !== "string" || !agentTok.startsWith("dj_agt_")) {
      fail(`create agent token missing: ${JSON.stringify(payload)}`);
    }
    console.log("manage_agent_tokens create ok");

    res = await callTool(client, "manage_agent_tokens", { action: "list" });
    payload = structuredPayload(res);
    assertArrayField(payload, "tokens", 1);
    console.log("manage_agent_tokens list populated ok");

    const machineID = randomUUID();
    const hb = await httpJSON(
      "POST",
      "/v1/heartbeat",
      {
        machine_id: machineID,
        agent_version: "0.0.1-mcp-sdk",
        hostname: "mcp-sdk-e2e",
        os_version: "linux",
        applied_policy_version: 0,
      },
      agentTok,
    );
    if (hb.status !== 200) fail(`heartbeat ${hb.status}: ${hb.text}`);

    res = await callTool(client, "list_machines", {});
    payload = structuredPayload(res);
    const machines = assertArrayField(payload, "machines", 1);
    if (!machines.some((m) => m.machine_id === machineID)) {
      fail(`enrolled machine ${machineID} not in list`);
    }
    console.log("list_machines populated ok");

    res = await callTool(client, "get_machine", { machine_id: machineID });
    payload = structuredPayload(res);
    if (payload.machine_id !== machineID) fail(`get_machine mismatch: ${JSON.stringify(payload)}`);
    console.log("get_machine ok");

    res = await callTool(client, "set_policy", {
      machine_id: machineID,
      expected_version: 0,
      document: { block: ["game.exe"] },
    });
    payload = structuredPayload(res);
    if (payload.version !== 1) fail(`set_policy version: ${JSON.stringify(payload)}`);
    console.log("set_policy ok");

    res = await callTool(client, "run_script", {
      machine_id: machineID,
      type: "cmd",
      payload: "echo mcp-sdk",
      expires_at: new Date(Date.now() + 3600_000).toISOString(),
    });
    payload = structuredPayload(res);
    const jobID = payload.id;
    if (!jobID) fail(`run_script missing id: ${JSON.stringify(payload)}`);
    console.log("run_script ok");

    res = await callTool(client, "get_job", { job_id: jobID });
    payload = structuredPayload(res);
    if (payload.status !== "pending" && payload.status !== "canceled") {
      // pending expected; tolerate cancel races
      console.log("get_job status:", payload.status);
    }
    console.log("get_job ok");

    res = await callTool(client, "list_events", { machine_id: machineID });
    payload = structuredPayload(res);
    assertArrayField(payload, "events", 0);
    console.log("list_events ok");

    res = await callTool(client, "manage_agent_tokens", { action: "revoke", id: tokID });
    payload = structuredPayload(res);
    if (payload.revoked !== true) fail(`revoke: ${JSON.stringify(payload)}`);
    console.log("manage_agent_tokens revoke ok");

    console.log("mcp-sdk e2e passed");
  } finally {
    await client.close().catch(() => {});
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
