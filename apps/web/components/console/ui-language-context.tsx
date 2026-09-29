"use client";

import { createContext, useContext } from "react";

import { type UiLanguage, type UiTextKey, uiText } from "@/lib/ui-language";

const UiLanguageContext = createContext<UiLanguage>("en");

export function UiLanguageProvider({ children, language }: { children: React.ReactNode; language: UiLanguage }) {
  return <UiLanguageContext.Provider value={language}>{children}</UiLanguageContext.Provider>;
}

export function useUiLanguage() {
  return useContext(UiLanguageContext);
}

export function useUiText() {
  const language = useUiLanguage();
  return (key: UiTextKey) => uiText(language, key);
}
