import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { Guard } from "@/app/Guard";
import { Badge, PageHeader, Section, Skeleton, TextField } from "@/shared/ui";
import { useDebounced } from "@/shared/useDebounced";
import s from "./apidocs.module.css";

/** Часть спецификации OpenAPI, нужная странице. */
interface SchemaObj {
  $ref?: string;
  type?: string | string[];
  format?: string;
  description?: string;
  enum?: unknown[];
  items?: SchemaObj;
  properties?: Record<string, SchemaObj>;
  required?: string[];
  allOf?: SchemaObj[];
  additionalProperties?: boolean | SchemaObj;
  minimum?: number;
  maximum?: number;
  default?: unknown;
}

interface Param {
  $ref?: string;
  name: string;
  in: string;
  required?: boolean;
  description?: string;
  schema?: SchemaObj;
}

interface Operation {
  operationId: string;
  summary?: string;
  description?: string;
  tags?: string[];
  "x-permission"?: string;
  parameters?: Param[];
  requestBody?: { content?: Record<string, { schema?: SchemaObj }> };
  responses?: Record<string, { $ref?: string; description?: string; content?: Record<string, { schema?: SchemaObj }> }>;
}

interface Spec {
  info: { title: string; version: string; description?: string };
  tags?: { name: string; description?: string }[];
  paths: Record<string, Record<string, Operation>>;
  components: { schemas: Record<string, SchemaObj>; parameters: Record<string, Param> };
}

const methods = ["get", "post", "put", "patch", "delete"] as const;

const permissionTitle: Record<string, string> = {
  authenticated: "любой вошедший",
};

export function ApiDocsPage() {
  return (
    <Guard permission="tokens.own">
      <ApiDocs />
    </Guard>
  );
}

const refName = (ref: string) => ref.split("/").pop() ?? ref;

function resolve(spec: Spec, sc?: SchemaObj): SchemaObj | undefined {
  if (!sc) {
    return undefined;
  }
  if (sc.$ref) {
    return resolve(spec, spec.components.schemas[refName(sc.$ref)]);
  }
  if (sc.allOf) {
    const parts = sc.allOf.map((p) => resolve(spec, p) ?? {});
    return {
      type: "object",
      description: sc.description,
      properties: Object.assign({}, ...parts.map((p) => p.properties ?? {})),
      required: parts.flatMap((p) => p.required ?? []),
    };
  }
  return sc;
}

/** Краткая запись типа: «Alert», «массив Alert», «строка (uuid)», «одно из: a, b». */
function typeText(sc?: SchemaObj): string {
  if (!sc) {
    return "—";
  }
  if (sc.$ref) {
    return refName(sc.$ref);
  }
  if (sc.allOf) {
    return sc.allOf.map(typeText).join(" + ");
  }
  if (sc.enum) {
    return `одно из: ${sc.enum.join(", ")}`;
  }
  const t = Array.isArray(sc.type) ? sc.type.join(" | ") : sc.type;
  if (t === "array") {
    return `массив ${typeText(sc.items)}`;
  }
  const names: Record<string, string> = {
    string: "строка",
    integer: "целое",
    number: "число",
    boolean: "да/нет",
    object: "объект",
  };
  const base = t ? (names[t] ?? t) : "объект";
  const range = sc.minimum !== undefined || sc.maximum !== undefined ? ` ${sc.minimum ?? ""}…${sc.maximum ?? ""}` : "";
  return `${base}${sc.format ? ` (${sc.format})` : ""}${range}`;
}

function SchemaTable({ spec, schema }: { spec: Spec; schema?: SchemaObj }) {
  const sc = resolve(spec, schema);
  if (!sc) {
    return null;
  }
  if (sc.type === "array") {
    return (
      <>
        <p className={s.note}>Массив {typeText(sc.items)}:</p>
        <SchemaTable spec={spec} schema={sc.items} />
      </>
    );
  }
  const props = Object.entries(sc.properties ?? {});
  if (props.length === 0) {
    return <p className={s.note}>{sc.description ?? typeText(sc)}</p>;
  }
  return (
    <table className={s.table}>
      <thead>
        <tr>
          <th scope="col">Поле</th>
          <th scope="col">Тип</th>
          <th scope="col">Описание</th>
        </tr>
      </thead>
      <tbody>
        {props.map(([name, p]) => (
          <tr key={name}>
            <td>
              <code>{name}</code>
              {sc.required?.includes(name) && <span className={s.req}> *</span>}
            </td>
            <td>{typeText(p)}</td>
            <td>{p.description ?? resolve(spec, p)?.description ?? ""}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function curl(method: string, path: string, op: Operation, origin: string): string {
  const body = op.requestBody?.content?.["application/json"];
  const lines = [`curl -X ${method.toUpperCase()} "${origin}/api/v1${path}"`, `  -H "Authorization: Bearer $FF_TOKEN"`];
  if (body) {
    lines.push(`  -H "Content-Type: application/json"`, `  -d '{…}'`);
  }
  return lines.join(" \\\n");
}

function OperationView({ spec, method, path, op }: { spec: Spec; method: string; path: string; op: Operation }) {
  const params = (op.parameters ?? [])
    .map((p) => (p.$ref ? spec.components.parameters[refName(p.$ref)] : p))
    .filter(Boolean) as Param[];
  const body = op.requestBody?.content?.["application/json"]?.schema;
  const ok = Object.entries(op.responses ?? {}).find(([code]) => code.startsWith("2"));
  const okSchema = ok?.[1].content?.["application/json"]?.schema;
  const perm = op["x-permission"] ?? "";
  return (
    <details className={s.op} id={op.operationId}>
      <summary>
        <span className={s.method} data-method={method}>
          {method.toUpperCase()}
        </span>
        <code className={s.path}>{path}</code>
        <span className={s.summary}>{op.summary}</span>
      </summary>
      <div className={s.opBody}>
        {op.description && <p className={s.description}>{op.description}</p>}
        <p className={s.note}>
          Право: <Badge plain>{permissionTitle[perm] ?? perm}</Badge> · операция <code>{op.operationId}</code>
        </p>
        {params.length > 0 && (
          <>
            <h4>Параметры</h4>
            <table className={s.table}>
              <thead>
                <tr>
                  <th scope="col">Имя</th>
                  <th scope="col">Где</th>
                  <th scope="col">Тип</th>
                  <th scope="col">Описание</th>
                </tr>
              </thead>
              <tbody>
                {params.map((p) => (
                  <tr key={`${p.in}-${p.name}`}>
                    <td>
                      <code>{p.name}</code>
                      {p.required && <span className={s.req}> *</span>}
                    </td>
                    <td>{p.in === "path" ? "путь" : p.in === "query" ? "запрос" : p.in}</td>
                    <td>{typeText(p.schema)}</td>
                    <td>{p.description ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
        {body && (
          <>
            <h4>Тело запроса — {typeText(body)}</h4>
            <SchemaTable spec={spec} schema={body} />
          </>
        )}
        {ok && (
          <>
            <h4>
              Ответ {ok[0]} — {ok[1].description}
              {okSchema ? ` (${typeText(okSchema)})` : ""}
            </h4>
            {okSchema && <SchemaTable spec={spec} schema={okSchema} />}
          </>
        )}
        <h4>Пример</h4>
        <pre className={s.code}>{curl(method, path, op, window.location.origin)}</pre>
      </div>
    </details>
  );
}

const verifyPython = `import hmac, hashlib, time

def verify(secret: str, timestamp: str, body: bytes, signature: str) -> bool:
    if abs(time.time() - int(timestamp)) > 300:  # не старше 5 минут
        return False
    mac = hmac.new(secret.encode(), timestamp.encode() + b"." + body, hashlib.sha256)
    return hmac.compare_digest("sha256=" + mac.hexdigest(), signature)`;

const verifyNode = `import { createHmac, timingSafeEqual } from "node:crypto";

export function verify(secret, timestamp, body, signature) {
  if (Math.abs(Date.now() / 1000 - Number(timestamp)) > 300) return false;
  const want = "sha256=" + createHmac("sha256", secret).update(\`\${timestamp}.\${body}\`).digest("hex");
  return want.length === signature.length && timingSafeEqual(Buffer.from(want), Buffer.from(signature));
}`;

const webhookSample = `POST https://example.com/freightflow
X-FreightFlow-Event: alert.created
X-FreightFlow-Delivery: 01a1…
X-FreightFlow-Timestamp: 1793716000
X-FreightFlow-Signature: sha256=5d1c…

{
  "id": "01a1…",
  "event": "alert.created",
  "sentAt": "2026-11-03T17:00:00Z",
  "data": {
    "event": "alert.created",
    "link": "https://…/alerts?alert=01a1…",
    "alert": { "id": "…", "title": "Аварийная работа (новая): …", "severity": "critical", "kind": "roadworks", … }
  }
}`;

function ApiDocs() {
  const spec = useQuery({
    queryKey: ["openapi"],
    queryFn: async () => {
      const r = await fetch("/api/v1/openapi.json");
      if (!r.ok) {
        throw new Error(`спецификация: ${r.status}`);
      }
      return (await r.json()) as Spec;
    },
    staleTime: 300_000,
  });
  const [text, setText] = useState("");
  const q = useDebounced(text.trim().toLowerCase(), 150);
  const groups = useMemo(() => {
    const sp = spec.data;
    if (!sp) {
      return [];
    }
    const byTag = new Map<string, { method: string; path: string; op: Operation }[]>();
    for (const [path, item] of Object.entries(sp.paths)) {
      for (const m of methods) {
        const op = item[m];
        if (!op) {
          continue;
        }
        const hay = `${path} ${op.summary ?? ""} ${op.operationId} ${op.description ?? ""}`.toLowerCase();
        if (q && !hay.includes(q)) {
          continue;
        }
        const tag = op.tags?.[0] ?? "other";
        byTag.set(tag, [...(byTag.get(tag) ?? []), { method: m, path, op }]);
      }
    }
    return (sp.tags ?? []).map((t) => ({ tag: t, ops: byTag.get(t.name) ?? [] })).filter((g) => g.ops.length > 0);
  }, [spec.data, q]);
  const count = groups.reduce((n, g) => n + g.ops.length, 0);
  const origin = window.location.origin;

  return (
    <div className={s.page}>
      <PageHeader
        title="API"
        subtitle="Программный доступ к данным и расчётам FreightFlow: те же операции, что использует интерфейс"
      />
      <Section title="Начало работы">
        <ol className={s.steps}>
          <li>
            Выпустите токен в <Link to="/profile">профиле</Link> («Токены API»): отметьте нужные права и срок действия.
            Значение показывается один раз.
          </li>
          <li>
            Передавайте его в заголовке <code>Authorization: Bearer &lt;токен&gt;</code>. Адрес API —{" "}
            <code>{origin}/api/v1</code>, спецификация OpenAPI 3 —{" "}
            <a href="/api/v1/openapi.json">/api/v1/openapi.json</a> (для генераторов клиентов).
          </li>
          <li>
            Лимит — 300 запросов подряд на токен, затем 5 в секунду; расчёты маршрутов — 60 подряд, затем 1 в секунду.
            Сверх лимита — ответ 429.
          </li>
          <li>
            Ошибки — <code>application/problem+json</code>: <code>status</code>, <code>title</code>, <code>detail</code>
            , <code>errors</code> по полям. Права операции указаны у каждой: токен получает пересечение прав владельца и
            выбранных при выпуске.
          </li>
          <li>
            События в реальном времени — поток Server-Sent Events <code>GET /api/v1/events</code> (новые алерты,
            уведомления, публикации данных, ход расчётов); после разрыва передайте <code>Last-Event-ID</code> —
            пропущенное будет дослано.
          </li>
        </ol>
        <pre
          className={s.code}
        >{`curl -H "Authorization: Bearer $FF_TOKEN" "${origin}/api/v1/facilities?type=warehouse&limit=10"`}</pre>
      </Section>

      <Section title="Вебхуки">
        <p>
          Вебхук передаёт новые алерты правил с каналом «Вебхук» во внешнюю систему: POST с JSON на ваш адрес HTTPS
          (адреса внутренней сети не принимаются). Добавляются в <Link to="/profile">профиле</Link>; секрет подписи
          показывается один раз. Ответ 2xx — доставлено; 4xx (кроме 408 и 429) — доставка прекращается; иначе — до 8
          повторов с растущей паузой. Журнал доставок — в профиле. Событие <code>ping</code> — пробная доставка.
        </p>
        <pre className={s.code}>{webhookSample}</pre>
        <p>
          Подпись: <code>X-FreightFlow-Signature = "sha256=" + hex(HMAC-SHA256(секрет, метка + "." + тело))</code>,
          метка — <code>X-FreightFlow-Timestamp</code> (секунды Unix). Проверяйте подпись и отбрасывайте запросы со
          старой меткой.
        </p>
        <div className={s.columns}>
          <div>
            <h4>Python</h4>
            <pre className={s.code}>{verifyPython}</pre>
          </div>
          <div>
            <h4>Node.js</h4>
            <pre className={s.code}>{verifyNode}</pre>
          </div>
        </div>
      </Section>

      <Section
        title="Операции"
        description={spec.data ? `Версия ${spec.data.info.version}; операций: ${count}` : undefined}
      >
        <TextField
          label="Найти операцию"
          placeholder="Путь, название, например «алерт» или /facilities"
          value={text}
          onChange={setText}
        />
        {spec.isPending ? (
          <Skeleton height={200} />
        ) : spec.error ? (
          <div className="notice notice-error" role="alert">
            Не удалось загрузить спецификацию.
          </div>
        ) : (
          groups.map((g) => (
            <section key={g.tag.name} className={s.group} aria-label={g.tag.description ?? g.tag.name}>
              <h3>{g.tag.description ?? g.tag.name}</h3>
              {g.ops.map((o) => (
                <OperationView
                  key={`${o.method} ${o.path}`}
                  spec={spec.data as Spec}
                  method={o.method}
                  path={o.path}
                  op={o.op}
                />
              ))}
            </section>
          ))
        )}
      </Section>
    </div>
  );
}
