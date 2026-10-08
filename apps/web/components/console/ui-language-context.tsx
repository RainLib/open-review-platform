"use client";

import { createContext, useCallback, useContext } from "react";

import { type UiLanguage, type UiTextKey, uiText } from "@/lib/ui-language";
import { workflowStatus, workflowText, type WorkflowMessageValues } from "@/lib/workflow-copy";

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

export function useWorkflowText() {
  const language = useUiLanguage();
  return useCallback((source: string, values?: WorkflowMessageValues) => workflowText(language, source, values), [language]);
}

export function useWorkflowStatus() {
  const language = useUiLanguage();
  return useCallback((state: string) => workflowStatus(language, state), [language]);
}
