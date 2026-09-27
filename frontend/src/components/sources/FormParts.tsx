import { Upload } from "lucide-react";
import { useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/** 通道与数据源表单共用的表单部件 */

/** 标签后附“可选” */
export function Optional({ label }: { label: ReactNode }) {
  const { t } = useTranslation();
  return (
    <>
      {label}
      <span className="ml-1.5 text-xs text-faint-foreground">{t("sources.optional")}</span>
    </>
  );
}

/** 表单顶部的类型切换（单选按钮组） */
export function KindTabs<K extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: K;
  options: { value: K; label: string; icon: ReactNode }[];
  onChange: (kind: K) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex w-fit gap-0.5 rounded-md bg-accent p-0.75">
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(o.value)}
            className={cn(
              "flex items-center gap-1.5 rounded-sm px-3 py-1.5 text-sm [&_svg]:size-4",
              selected ? "bg-card font-semibold text-foreground" : "text-muted-foreground hover:text-foreground"
            )}
          >
            {o.icon}
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

/** 私钥、证书等 PEM 文本：可粘贴，也可上传文件读入；有错误时显示错误，否则显示 hint */
export function PemField({
  label,
  optional = false,
  value,
  placeholder,
  rows = 2,
  error,
  hint,
  onChange,
}: {
  label: string;
  optional?: boolean;
  value: string;
  placeholder?: string;
  rows?: number;
  error?: string;
  hint?: string;
  onChange: (text: string) => void;
}) {
  const { t } = useTranslation();
  const fileInput = useRef<HTMLInputElement>(null);
  const readFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => onChange(String(reader.result ?? ""));
    reader.readAsText(file);
  };
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between">
        <Label className="font-normal text-muted-foreground">{optional ? <Optional label={label} /> : label}</Label>
        <button
          type="button"
          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          onClick={() => fileInput.current?.click()}
        >
          <Upload className="size-3.5" />
          {t("sources.upload")}
        </button>
        <input
          ref={fileInput}
          type="file"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) readFile(file);
            e.target.value = "";
          }}
        />
      </div>
      <textarea
        value={value}
        placeholder={placeholder}
        aria-label={label}
        aria-invalid={error ? true : undefined}
        rows={rows}
        className={cn(
          "w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-xs outline-none",
          "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
        )}
        onChange={(e) => onChange(e.target.value)}
      />
      {error ? (
        <p className="text-xs text-destructive">{error}</p>
      ) : (
        hint && <p className="text-xs text-faint-foreground">{hint}</p>
      )}
    </div>
  );
}
