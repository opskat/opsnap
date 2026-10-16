/** 把文本作为文件下载到本机（不经过服务器） */
export function downloadText(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // 点击后浏览器异步开始下载，同步回收可能让下载拿不到内容
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

/**
 * 让浏览器直接下载一个地址。响应是流式的，不经过 fetch，也不在内存里缓冲：
 * 调用前必须先用预检接口确认能下载，否则失败时浏览器只会显示一个失败的下载。
 */
export function downloadUrl(url: string) {
  const a = document.createElement("a");
  a.href = url;
  // 带 download：出错时服务端返回的 JSON 只会成为一个失败的下载，不会把单页应用导航走
  a.download = "";
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
}
