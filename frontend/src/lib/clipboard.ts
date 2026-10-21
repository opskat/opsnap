// 写剪贴板。面板默认以 http 在内网访问，而 navigator.clipboard 只在安全上下文
//（https 或 localhost）里存在，所以除了异步 Clipboard API 还要留一条旧通路的退路。

/**
 * 旧通路：临时放一个只读文本框，选中后走 document.execCommand("copy")。
 * 非安全上下文里这是唯一还能用的办法；不支持时返回 false，由调用方提示用户手动复制。
 */
function legacyCopy(text: string): boolean {
  if (typeof document.execCommand !== "function" || !document.body) return false;

  const area = document.createElement("textarea");
  area.value = text;
  // 只读，避免移动端弹键盘；放到屏幕外但仍在文档里，选中才有效
  area.setAttribute("readonly", "");
  Object.assign(area.style, {
    position: "fixed",
    top: "0",
    left: "-9999px",
    width: "1px",
    height: "1px",
    opacity: "0",
  });

  const previous = document.activeElement as HTMLElement | null;
  document.body.appendChild(area);
  try {
    area.select();
    area.setSelectionRange(0, text.length);
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
    previous?.focus?.();
  }
}

/**
 * 把文字写进剪贴板。先用 Clipboard API，不存在的或被拒绝时退回旧通路；
 * 两条都不行返回 false，调用方据此提示「请手动选中后复制」。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator.clipboard?.writeText === "function") {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 权限被拒或非安全上下文：继续试旧通路
  }
  return legacyCopy(text);
}
