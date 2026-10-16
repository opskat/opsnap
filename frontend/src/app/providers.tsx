import * as React from "react";
import { ThemeProvider } from "@/lib/theme";
import { AuthProvider } from "@/lib/auth";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";

/** 面板的全部 Provider；需要放在 Router 里面（AuthProvider 用到路由跳转） */
export function AppProviders({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider>
      <AuthProvider>
        <TooltipProvider delayDuration={150}>
          {children}
          <Toaster position="bottom-right" />
        </TooltipProvider>
      </AuthProvider>
    </ThemeProvider>
  );
}
