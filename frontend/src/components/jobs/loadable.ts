/** 区域内异步加载的三种状态 */
export type Loadable<T> = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; data: T };

export const errorMessage = (err: unknown) => (err instanceof Error ? err.message : String(err));
