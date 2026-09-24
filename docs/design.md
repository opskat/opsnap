# OpsNap design system

Source of truth: the design file `opsnap.pen` (the "Terminal" dark style with light and dark variable sets). In code, tokens live in `frontend/src/styles/globals.css` and theme logic in `frontend/src/lib/theme.tsx`. Enforcement belongs to [`develop.md`](develop.md#enforced-rules); implementation layering to [`architecture.md`](architecture.md).

## Core constraints

- Use only the semantic tokens in `globals.css`. No Tailwind palette classes (`bg-red-500`, `text-white`, `bg-black/50`), no arbitrary colours (`bg-[#fff]`), no colour literals in `style`. A new concept gets both a light and a dark value and a row in the token table below. — enforced by `opsnap/no-raw-color`
- Use only the type scale below; no arbitrary font sizes such as `text-[13px]`. — enforced by `opsnap/no-arbitrary-font-size`
- Theme differences live in token values. App code writes one set of classes and never uses `dark:` variants; shadcn components under `src/components/ui/` keep their upstream `dark:` variants, which only reference tokens. — enforced by `opsnap/no-dark-variant`
- **Text on a green fill uses `primary`** (`#08804F` with white text, about 5:1 contrast). The brand mint `brand` is for the logo mark, emphasised numbers, charts and progress only. — review-only
- Numbers, addresses, paths, durations and sizes use `font-mono`. — review-only
- Reuse `frontend/src/components/ui/` (shadcn/ui) and `frontend/src/components/layout/` before adding components; icons come from `lucide-react`. — review-only
- Merge classes with `cn()` (`frontend/src/lib/utils.ts`); express variants with `class-variance-authority`. Inline styles only for values CSS classes cannot express. — review-only
- Every async flow a component owns covers loading, error and success, and replaces only the region that changed. — review-only
- All static UI copy goes through i18n; user data, runtime content and logs are never translated. — enforced by `i18next/no-literal-string` and `check-i18n.mjs`
- Check every UI state in both light and dark themes and in both languages. — review-only

## Theme and tokens

`ThemeProvider` supports light, dark and system modes, stores the choice in `localStorage` (`opsnap-theme`), and toggles the `dark` class on `<html>`. An inline script in `frontend/index.html` applies the theme before the first paint so dark-mode users do not see a white flash on reload; keep it in sync with `theme.tsx`.

| Token | Light | Dark | Use |
|---|---|---|---|
| `background` | `#F6F7F7` | `#0B0D0E` | page background |
| `foreground` | `#0E1113` | `#E8ECEF` | body text |
| `card` / `popover` | `#FFFFFF` | `#121619` | card and overlay surfaces |
| `sidebar` | `#FFFFFF` | `#0F1214` | sidebar |
| `muted` / `secondary` / `accent` | `#F0F2F3` | `#1A2024` | secondary fills, hover, selected item |
| `muted-foreground` | `#5E6873` | `#86909A` | secondary text |
| `faint-foreground` | `#66707A` | `#7D8790` | weaker text (inactive nav items, group labels) |
| `border` / `input` | `#E2E6E9` | `#20272C` | borders and inputs |
| `ring` | `#1FBF7F` | `#3DDC97` | focus ring |
| `primary` / `primary-foreground` | `#08804F` / `#FFFFFF` | `#08804F` / `#FFFFFF` | solid buttons and any text on green |
| `brand` / `brand-foreground` | `#1FBF7F` / `#04140C` | `#3DDC97` / `#04140C` | logo mark fill and the icon on it |
| `brand-text` | `#0B8F5A` | `#3DDC97` | brand-coloured text (emphasised numbers) |
| `success` / `success-soft` | `#087A4B` / `#0E9F6317` | `#3DDC97` / `#3DDC971F` | success text / status badge fill |
| `running` / `running-soft` | `#1765C2` / `#1F7AE017` | `#5AB0FF` / `#5AB0FF1F` | running |
| `warning` / `warning-soft` | `#8A5A00` / `#8A5A0017` | `#F5B83D` / `#F5B83D1F` | usable with risk |
| `destructive` / `destructive-soft` | `#C4313A` / `#DC3B4117` | `#FF5C5C` / `#FF5C5C1F` | failure, dangerous actions |
| `pending` / `pending-soft` | `#5E6873` / `#7A848D17` | `#86909A` / `#86909A1F` | queued |
| `chart-bar` | `#5FD3A1` | `#2E8F66` | successful bars in charts |
| `track` | `#ECEFF1` | `#1E2529` | progress bar track |
| `overlay` | `#0507084D` | `#05070899` | dialog backdrop |

Radius: `--radius: 0.375rem` (6px); `rounded-sm` / `rounded-md` / `rounded-lg` derive from it.

## Typography

Fonts: UI text uses `Geist Variable`; numbers, addresses and durations use `JetBrains Mono Variable` (`font-mono`); Chinese falls back to PingFang SC / Microsoft YaHei. Both fonts are bundled through `@fontsource-variable`; nothing loads from a CDN.

The type scale overrides Tailwind's defaults in `globals.css` and matches the sizes used in the design file:

| Class | Size / line height | Use |
|---|---|---|
| `text-2xs` | 11px / 16px | group labels, dense metadata |
| `text-xs` | 12px / 16px | hints, badges, secondary lines |
| `text-sm` | 13px / 20px | body text, buttons, nav items (default UI size) |
| `text-base` | 14px / 20px | section titles |
| `text-md` | 15px / 22px | card titles |
| `text-lg` | 17px / 24px | wordmark, dialog titles |
| `text-xl` | 20px / 28px | wizard page titles |
| `text-2xl` | 24px / 32px | page titles |
| `text-3xl` | 26px / 32px | stat values |

## Layout

- App frame: `AppShell` (`frontend/src/components/layout/AppShell.tsx`) — a 232px sidebar on the left and an independently scrolling content area.
- Navigation items are defined in `frontend/src/components/layout/nav.ts` (main navigation plus the "system" group). The bottom of the sidebar shows the signed-in username with a sign-out button, then the theme and language switches (`PreferenceToggles`).
- Page titles use `PageHeader` (title, optional subtitle, bottom divider).
- Sign-in and first-run setup pages use `AuthLayout`: theme and language switches top right, brand, title and subtitle, then a centred 384px card holding the form, and an optional footnote.

## Components and states

| Need | Project pattern |
|---|---|
| First load | "Loading…" text inside the region; the surrounding card keeps its position when content replaces it |
| Error | message on a `destructive-soft` fill inside the failing region, with a Retry button |
| Status badge | `<status>-soft` fill + `<status>` text + a dot of the same colour, e.g. the health badge "Healthy / Unavailable" |
| Form field | `FormField` (`frontend/src/components/form/FormField.tsx`): label above, hint below in `faint-foreground`, which an error in `destructive` replaces (and the input gets `aria-invalid`); `type="password"` adds a show/hide toggle; `mono` for codes, usernames and secrets |
| Form-level error | `role="alert"` message on a `destructive-soft` fill at the top of the form |
| Submitting | the submit button is disabled and reads "Submitting…" |
| Switch | `Switch` (`frontend/src/components/form/Switch.tsx`): `role="switch"` with an `aria-label`; used for on/off settings such as HTTPS and password sign-in |
| Row actions | at most two ghost buttons in the row, the rest in a `DropdownMenu` (`frontend/src/components/ui/dropdown-menu.tsx`) behind an ⋯ icon button whose label names the row; destructive items last, after a separator |
| Destructive or irreversible step | a confirmation dialog stating what is and is not affected; a required checkbox when the user must have done something first (e.g. saved a key) |
| Page not built yet | `ComingSoonPage`: keeps navigation complete; replace it when the feature lands |

Reference implementation: `frontend/src/pages/OverviewPage.tsx` (loading, error with retry, ready; the region has `aria-live="polite"`).

## Accessibility

- Text meets WCAG AA in every theme: normal text ≥ 4.5:1, icons ≥ 3:1. The design file's colours were checked against this. Meaning is never carried by colour alone; statuses also have text.
- Icon-only buttons have an `aria-label` (e.g. the theme switches); toggle buttons expose state with `aria-pressed`.
- Loading, error and success changes are announced through an `aria-live` region.

## Adding a page

1. Add the navigation item in `nav.ts` and the route in `App.tsx`.
2. Compose the page from `PageHeader`, existing components, tokens and the type scale.
3. Implement the async states the page owns.
4. Check light and dark themes and both languages; confirm keyboard access and labelled icon controls.
5. Run `make lint` and `make test`; verify real flows through [`verification.md`](verification.md).

## Sources

- Tokens and themes: `frontend/src/styles/globals.css`, `frontend/src/lib/theme.tsx`, `frontend/index.html`
- Components: `frontend/src/components/ui/`, `frontend/src/components/layout/`
- Rules: `frontend/eslint-rules/`, [`develop.md`](develop.md#enforced-rules)
- Fact checks: [`documentation.md`](documentation.md)
