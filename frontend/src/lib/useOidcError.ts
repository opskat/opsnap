import { useTranslation } from "react-i18next";

import type { OidcErrorKind } from "@/lib/oidc";

const kinds: OidcErrorKind[] = ["not_bound", "idp", "invalid", "unreachable", "already_bound"];

/** 把跳回页面时带的 oidc_error / oidc_error_description 转成提示文案；没有错误时返回 undefined */
export function useOidcErrorMessage(params: URLSearchParams) {
  const { t } = useTranslation();
  const kind = params.get("oidc_error") as OidcErrorKind | null;
  if (!kind || !kinds.includes(kind)) return undefined;
  return t(`oidc.error.${kind}`, { description: params.get("oidc_error_description") ?? "" });
}
