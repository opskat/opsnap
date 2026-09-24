import i18n from "@/i18n";

// 后端（cago）统一响应格式：code 为 0 表示成功
interface Envelope<T> {
  code: number;
  msg: string;
  data: T;
}

export class ApiError extends Error {
  readonly code: number;
  readonly status: number;

  constructor(code: number, message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

let unauthorizedHandler: (() => void) | undefined;

/** 注册会话失效（任意接口返回 401）时的处理，返回取消注册的函数 */
export function onUnauthorized(handler: () => void) {
  unauthorizedHandler = handler;
  return () => {
    if (unauthorizedHandler === handler) unauthorizedHandler = undefined;
  };
}

/** 所有接口请求的唯一入口：拼接 /api/v1 前缀、携带界面语言、把失败统一转换为 ApiError */
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "Accept-Language": i18n.language,
      ...init?.headers,
    },
  });
  let body: Envelope<T> | undefined;
  try {
    body = (await res.json()) as Envelope<T>;
  } catch {
    body = undefined;
  }
  if (res.status === 401) unauthorizedHandler?.();
  if (!res.ok || !body || body.code !== 0) {
    throw new ApiError(body?.code ?? -1, body?.msg || res.statusText, res.status);
  }
  return body.data;
}
