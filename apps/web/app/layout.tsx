import type { Metadata } from "next";

import { TooltipProvider } from "@/components/ui/tooltip";
import { UiLanguageProvider } from "@/components/console/ui-language-context";
import { getUiLanguage } from "@/lib/ui-language-server";

import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Open Review — evidence-first AI code review",
    template: "%s — Open Review",
  },
  description:
    "A durable, self-hostable control plane for evidence-first AI code review.",
};

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const language = await getUiLanguage();
  return (
    <html lang={language} className="dark">
      <body className="antialiased">
        <UiLanguageProvider language={language}><TooltipProvider>{children}</TooltipProvider></UiLanguageProvider>
      </body>
    </html>
  );
}
