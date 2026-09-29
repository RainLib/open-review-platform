import { cookies } from "next/headers";

import { normalizeUiLanguage, uiLanguageCookie } from "@/lib/ui-language";

export async function getUiLanguage() {
  return normalizeUiLanguage((await cookies()).get(uiLanguageCookie)?.value);
}
