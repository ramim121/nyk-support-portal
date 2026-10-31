// ─────────────────────────────────────────────────────────────────────────
//  BRANDING — one place to rebrand the console.
//   • Logo:   replace public/logo.svg (your logo already dropped in here).
//   • Colors: the `--brand-*` tokens at the top of style.css.
//   • Text:   the strings below.
// ─────────────────────────────────────────────────────────────────────────
export const BRAND = {
  /** Browser tab title. */
  productName: "NYK Advance Support",
  /** Small label shown beside the logo in the top bar. */
  consoleLabel: "Support Console",
  /** One-line tagline under the logo on the sign-in screen. */
  tagline: "Secure remote support — technician sign in",
  /** Logo file in public/ (your wordmark; contains the company name). */
  logoSrc: "logo.svg",
};

/** applyBrand injects brand text/title/logo into the page at startup. */
export function applyBrand() {
  document.title = BRAND.productName;
  setSrc("brand-logo", BRAND.logoSrc);
  setSrc("brand-logo-top", BRAND.logoSrc);
  setText("brand-tag-top", BRAND.consoleLabel);
  setText("login-sub", BRAND.tagline);
}

function setText(id: string, text: string) {
  const el = document.getElementById(id);
  if (el) el.textContent = text;
}
function setSrc(id: string, src: string) {
  const el = document.getElementById(id) as HTMLImageElement | null;
  if (el) el.src = src;
}
