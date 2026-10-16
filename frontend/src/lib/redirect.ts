/** 整页跳转到站外（例如单点登录的授权页）。包在对象里，测试可以替换 */
export const redirect = {
  to(url: string) {
    window.location.assign(url);
  },
};
