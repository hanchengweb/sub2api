# 成为代理 · Design QA

Final result: **passed**

## Reference and scope

- Selected reference: option 1, `C:/Users/m1358/.codex/generated_images/01a08b4a-d0dd-7b42-83d9-378c7ec4ae8d/exec-18e88613-5f6a-4ae4-ade5-7d6fc307ab43.png` (1487 × 1058).
- User correction: keep the selected design, remove the repeated page title and supporting small print; do not regenerate images.
- Implementation: `/become-agent`, using the existing authenticated layout, navigation, icons, configuration and clipboard helper. Existing data, pricing, model names and referral behavior are unchanged.
- Intentional differences: the topbar retains the page name; the content begins with the partnership headline, without another “成为代理” title or marketing subheading. Hero height and section copy are reduced. Existing product icons replace reference illustrations inside the selection cards.
- Bridge artwork is cropped from the approved reference at `(810, 218, 1487, 478)` into a 677 × 260 WebP, with feathered edges. No new image was generated.

## Visual evidence

Artifacts are in `C:/Users/m1358/Documents/Codex/2026-09-10/codex-legacy-consolidation-20260806/outputs/`:

| Evidence | Capture |
| --- | --- |
| Desktop | `agency-desktop.png`, 1487 × 1058 viewport, 1× |
| Mobile | `agency-mobile.png`, 390px viewport, full page, 1× |
| Tablet | `agency-tablet.png`, 768px viewport, full page, 1× |
| Source and implementation | `agency-comparison.png`, 2974 × 1090 |
| Focused form comparison | `agency-form-comparison.png` |
| Mobile dialog | `agency-mobile-dialog.png` |
| Dark mode | `agency-dark.png` |

Typography, spacing, palette, asset quality and copy were compared against option 1. The teal headline, pale background, bridge artwork, thin dividers, rounded controls and two-column form preserve its visual direction. Mobile stacks the artwork, choices and form without horizontal overflow. Section labels have a consistent hierarchy and no redundant introduction.

During review, the inherited teal mesh background was removed for this page, the hero and artwork scale were corrected, and image edges were feathered. Dark-mode selectors were corrected and actual heading color and image blend mode verified. Stable screenshots replaced captures taken during the existing layout transition. Final desktop/mobile and side-by-side images were visually inspected after these fixes.

## Behavior and checks

- Direction selection updates the cooperation brief and scenario placeholder.
- Native required/email validation and whitespace-only rejection prevent invalid brief generation.
- The native dialog supports focus handling, Escape, editing return and readable text.
- Copy success was verified in the browser; failure retains the brief for manual copying and is covered by the focused test.
- Contact details use existing platform configuration as escaped text; missing configuration produces an accurate empty state.
- Desktop, tablet and mobile: no horizontal overflow. Mobile dialog and dark mode verified. Browser error logs: empty.
- `npm run build`: passed, including `vue-tsc -b` and Vite. Existing bundle-size/Browserslist warnings remain non-blocking.
- Focused ESLint: passed.
- `vitest run src/views/user/__tests__/AgencyView.test.ts`: 2 passed; runtime-only i18n test compiler emits a non-blocking experimental warning.
- `git diff --check`: passed.

## Delivery boundary

Preview: `http://127.0.0.1:4317/become-agent`, using the local preview login fixture. This verifies the frontend, not production or a real application-submission backend. The form prepares and copies a cooperation brief; it does not claim submission, approval, commission rates or guaranteed earnings. Production has not been deployed.
