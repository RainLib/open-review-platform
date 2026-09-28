package issuetriage

import (
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// issueTriageCopy keeps provider-visible framework text in the same admitted
// language boundary as model output. The model never owns these status labels:
// an incomplete or adversarial Issue must not be able to alter their meaning.
type issueTriageCopy struct {
	language string

	ackStarted, ackReviewing, status, revision, scope, analyzing, issueContext, acknowledgementBoundary string
	completed, completedWithGaps, moreContextRequired                                                   string
	sufficient, partial, insufficient                                                                   string
	signal, result, contextQuality, evidenceBoundary, evidenceScope                                     string
	assessment, missingContext, noMissingContext                                                        string
	acceptanceCriteria, defaultAcceptance                                                               string
	riskSummary, riskAndImpact, affectedAreas, noAffectedAreas                                          string
	fileLinkBoundary                                                                                    string
	nextSteps, defaultNextSteps                                                                         string
	provenance, modelRoute, promptPolicy, issueFormatPolicy                                             string
	feedbackQuestion, feedbackDetail                                                                    string
	failureTitle, failureDetail, failureBoundary                                                        string
	viewAnalysis                                                                                        string
}

func issueTriageCopyFor(job domain.ProviderIssueAnalysisJob) issueTriageCopy {
	return issueTriageCopyForLanguage(resolveIssueTriageLanguage(effectiveIssueTriageConfig(job).Language, job.Title, job.Body))
}

// resolveIssueTriageLanguage is deliberately conservative for inherit. Explicit
// policy always wins; otherwise the framework follows scripts that can be
// recognized without sending Issue text to another service. Ambiguous Latin text
// retains English framework labels while the model follows its trusted prompt.
func resolveIssueTriageLanguage(configured, title, body string) string {
	switch strings.TrimSpace(configured) {
	case "zh-CN", "ja", "es", "en":
		return strings.TrimSpace(configured)
	}
	text := title + "\n" + body
	for _, value := range text {
		if value >= 0x3040 && value <= 0x30ff {
			return "ja"
		}
	}
	for _, value := range text {
		if value >= 0x4e00 && value <= 0x9fff {
			return "zh-CN"
		}
	}
	if strings.ContainsAny(text, "¿¡ñÑáéíóúüÁÉÍÓÚÜ") {
		return "es"
	}
	return "en"
}

func issueTriageCopyForLanguage(language string) issueTriageCopy {
	copy := englishIssueTriageCopy()
	copy.language = language
	switch language {
	case "zh-CN":
		copy = issueTriageCopy{
			language:   "zh-CN",
			ackStarted: "Issue 分析已开始", ackReviewing: "正在审阅 **%s#%d** 的上下文。分析完成后，本评论会原地更新。", status: "状态", revision: "修订", scope: "范围", analyzing: "分析中", issueContext: "仅 Issue 上下文", acknowledgementBoundary: "_此确认仅表示 Issue 已被接纳，并不构成代码、运行时或安全结论。_",
			completed: "Issue 分析已完成", completedWithGaps: "Issue 分析已完成，但仍有上下文缺口", moreContextRequired: "需要更多上下文",
			sufficient: "充分", partial: "部分充分", insufficient: "不足",
			signal: "信号", result: "结果", contextQuality: "上下文质量", evidenceBoundary: "证据边界", evidenceScope: "仅限 Issue 标题、描述、标签和可信工作区上下文",
			assessment: "分析结论", missingContext: "缺失上下文", noMissingContext: "根据 Issue 描述，未发现实质性上下文缺口。",
			acceptanceCriteria: "建议的验收标准", defaultAcceptance: "开始实施前，请补充可验证的验收标准。",
			riskSummary: "风险、影响与受影响区域", riskAndImpact: "风险与影响", affectedAreas: "可能受影响的区域", noAffectedAreas: "仅凭 Issue 描述，尚无法识别受影响组件。",
			fileLinkBoundary: "文件链接仅用于打开当前默认分支；本次分析没有检查该文件或固定其代码版本。",
			nextSteps:        "建议的后续步骤", defaultNextSteps: "请与 Issue 作者确认预期行为和复现证据。",
			provenance: "分析溯源", modelRoute: "模型路由", promptPolicy: "提示词策略", issueFormatPolicy: "Issue 格式策略",
			feedbackQuestion: "本次分析是否有帮助？", feedbackDetail: "请用 👍 或 👎 反馈。连接的 Provider 支持反馈投递时，该反馈只会被记录，绝不会再次触发分析。编辑 Issue 标题或描述会创建新的修订。",
			failureTitle: "Issue 分析暂不可用", failureDetail: "**%s#%d** 的分析未能完成。请求仍被保留，模型路由恢复健康后可以重试。", failureBoundary: "未从不完整的模型响应中发布任何代码或运行时结论。",
			viewAnalysis: "在 Open Review 查看分析与操作",
		}
	case "ja":
		copy = issueTriageCopy{
			language:   "ja",
			ackStarted: "Issue 分析を開始しました", ackReviewing: "**%s#%d** のコンテキストを確認しています。分析完了後、このコメントは同じ場所で更新されます。", status: "状態", revision: "リビジョン", scope: "範囲", analyzing: "分析中", issueContext: "Issue コンテキストのみ", acknowledgementBoundary: "_この確認は Issue が受理されたことだけを示し、コード、実行時、セキュリティの結論ではありません。_",
			completed: "Issue 分析が完了しました", completedWithGaps: "Issue 分析は完了しましたが、不足情報があります", moreContextRequired: "追加のコンテキストが必要です",
			sufficient: "十分", partial: "一部不足", insufficient: "不足",
			signal: "シグナル", result: "結果", contextQuality: "コンテキスト品質", evidenceBoundary: "証拠の境界", evidenceScope: "Issue のタイトル、説明、ラベル、および信頼されたワークスペースコンテキストのみ",
			assessment: "評価", missingContext: "不足しているコンテキスト", noMissingContext: "Issue の説明から重大なコンテキスト不足は確認されませんでした。",
			acceptanceCriteria: "提案された受け入れ基準", defaultAcceptance: "実装を始める前に、検証可能な受け入れ基準を追加してください。",
			riskSummary: "リスク、影響、影響範囲", riskAndImpact: "リスクと影響", affectedAreas: "影響を受ける可能性のある領域", noAffectedAreas: "Issue の説明だけでは、影響を受けるコンポーネントを特定できません。",
			fileLinkBoundary: "ファイルリンクは現在のデフォルトブランチへの移動用です。この分析はファイルを調査せず、コードのリビジョンも固定していません。",
			nextSteps:        "推奨される次の手順", defaultNextSteps: "期待される動作と再現証拠を Issue 作成者と確認してください。",
			provenance: "分析の来歴", modelRoute: "モデルルート", promptPolicy: "プロンプトポリシー", issueFormatPolicy: "Issue 形式ポリシー",
			feedbackQuestion: "この分析は役に立ちましたか？", feedbackDetail: "👍 または 👎 でフィードバックしてください。接続先 Provider がリアクションフィードバックを配信できる場合でも、記録のみで再分析は開始しません。Issue のタイトルまたは説明を編集すると新しいリビジョンが作成されます。",
			failureTitle: "Issue 分析を利用できません", failureDetail: "**%s#%d** の分析を完了できませんでした。要求は記録されたままで、モデルルートが正常になった後に再試行できます。", failureBoundary: "不完全なモデル応答からコードまたは実行時の結論は公開されませんでした。",
			viewAnalysis: "Open Review で分析と操作を表示",
		}
	case "es":
		copy = issueTriageCopy{
			language:   "es",
			ackStarted: "Análisis de la incidencia iniciado", ackReviewing: "Revisando el contexto de **%s#%d**. Este comentario se actualizará en el mismo lugar cuando termine el análisis.", status: "Estado", revision: "Revisión", scope: "Alcance", analyzing: "Analizando", issueContext: "Solo contexto de la incidencia", acknowledgementBoundary: "_Esta confirmación solo indica que la incidencia fue admitida; no es una conclusión de código, tiempo de ejecución ni seguridad._",
			completed: "Análisis de la incidencia completado", completedWithGaps: "Análisis de la incidencia completado con información faltante", moreContextRequired: "Se requiere más contexto",
			sufficient: "Suficiente", partial: "Parcial", insufficient: "Insuficiente",
			signal: "Señal", result: "Resultado", contextQuality: "Calidad del contexto", evidenceBoundary: "Límite de evidencia", evidenceScope: "Solo título, descripción, etiquetas y contexto confiable del espacio de trabajo de la incidencia",
			assessment: "Evaluación", missingContext: "Contexto faltante", noMissingContext: "No se identificó una falta material de contexto en la descripción de la incidencia.",
			acceptanceCriteria: "Criterios de aceptación propuestos", defaultAcceptance: "Agregue criterios de aceptación verificables antes de iniciar la implementación.",
			riskSummary: "Riesgo, impacto y áreas afectadas", riskAndImpact: "Riesgo e impacto", affectedAreas: "Áreas posiblemente afectadas", noAffectedAreas: "El componente afectado todavía no puede identificarse solo con la descripción de la incidencia.",
			fileLinkBoundary: "Los enlaces de archivos solo abren la rama predeterminada actual; este análisis no inspeccionó el archivo ni fijó una revisión del código.",
			nextSteps:        "Próximos pasos recomendados", defaultNextSteps: "Confirme el comportamiento esperado y la evidencia de reproducción con la persona autora de la incidencia.",
			provenance: "Procedencia del análisis", modelRoute: "Ruta del modelo", promptPolicy: "Política de prompt", issueFormatPolicy: "Política de formato de incidencia",
			feedbackQuestion: "¿Fue útil este análisis?", feedbackDetail: "Reaccione con 👍 o 👎. Cuando el Provider conectado admite la entrega de reacciones, se registra solo como feedback y nunca inicia otro análisis. Editar el título o la descripción crea una nueva revisión.",
			failureTitle: "Análisis de la incidencia no disponible", failureDetail: "No se pudo completar el análisis de **%s#%d**. La solicitud sigue registrada y puede reintentarse cuando la ruta del modelo esté disponible.", failureBoundary: "No se publicó ninguna conclusión de código o tiempo de ejecución a partir de una respuesta de modelo incompleta.",
			viewAnalysis: "Ver análisis y acciones en Open Review",
		}
	}
	return copy
}

func englishIssueTriageCopy() issueTriageCopy {
	return issueTriageCopy{
		language:   "en",
		ackStarted: "Issue analysis started", ackReviewing: "Reviewing the context for **%s#%d**. This comment will be updated in place when the analysis completes.", status: "Status", revision: "Revision", scope: "Scope", analyzing: "Analyzing", issueContext: "Issue context only", acknowledgementBoundary: "_This acknowledgement means the Issue was admitted. It is not a code, runtime, or security verdict._",
		completed: "Issue analysis completed", completedWithGaps: "Issue analysis completed with gaps", moreContextRequired: "More context required",
		sufficient: "Sufficient", partial: "Partial", insufficient: "Insufficient",
		signal: "Signal", result: "Result", contextQuality: "Context quality", evidenceBoundary: "Evidence boundary", evidenceScope: "Issue title, description, labels, and trusted workspace context",
		assessment: "Assessment", missingContext: "Missing context", noMissingContext: "No material context gap was identified from the Issue description.",
		acceptanceCriteria: "Proposed acceptance criteria", defaultAcceptance: "Add testable acceptance criteria before implementation begins.",
		riskSummary: "Risk, impact, and affected areas", riskAndImpact: "Risk and impact", affectedAreas: "Potentially affected areas", noAffectedAreas: "The affected component is not yet identifiable from the Issue description.",
		fileLinkBoundary: "File links only navigate to the current default branch; this analysis did not inspect the file or pin a code revision.",
		nextSteps:        "Recommended next steps", defaultNextSteps: "Confirm the expected behavior and reproduction evidence with the Issue author.",
		provenance: "Analysis provenance", modelRoute: "Model route", promptPolicy: "Prompt policy", issueFormatPolicy: "Issue format policy",
		feedbackQuestion: "Was this analysis useful?", feedbackDetail: "React with 👍 or 👎. When the connected provider supports reaction feedback delivery, it is recorded as feedback only and never starts another analysis. Editing the Issue title or description creates a new revision.",
		failureTitle: "Issue analysis unavailable", failureDetail: "The analysis for **%s#%d** could not be completed. The request remains recorded and can be retried after the model route is healthy.", failureBoundary: "No code or runtime conclusion was published from an incomplete model response.",
		viewAnalysis: "View analysis and actions in Open Review",
	}
}

func issueRequiredSectionMissing(language, section string) string {
	label := issueRequiredSectionLabel(language, section)
	switch language {
	case "zh-CN":
		return "缺少必填 Issue 章节：" + label + "。"
	case "ja":
		return "必須の Issue セクションがありません：" + label + "。"
	case "es":
		return "Falta la sección obligatoria de la incidencia: " + label + "."
	default:
		return "Required Issue section is missing: " + label + "."
	}
}

func issueRequiredSectionLabel(language, section string) string {
	labels := map[string]map[string]string{
		"zh-CN": {"outcome": "结果", "reproduction": "复现", "expected_behavior": "预期行为", "observed_behavior": "实际行为", "evidence": "证据", "acceptance_criteria": "验收标准", "risk": "风险", "security_impact": "安全影响", "impact": "影响", "timeline": "时间线", "detection": "发现方式", "mitigation": "缓解措施", "non_goals": "非目标"},
		"ja":    {"outcome": "結果", "reproduction": "再現手順", "expected_behavior": "期待される動作", "observed_behavior": "実際の動作", "evidence": "証拠", "acceptance_criteria": "受け入れ基準", "risk": "リスク", "security_impact": "セキュリティ影響", "impact": "影響", "timeline": "タイムライン", "detection": "検知", "mitigation": "緩和策", "non_goals": "対象外"},
		"es":    {"outcome": "resultado", "reproduction": "reproducción", "expected_behavior": "comportamiento esperado", "observed_behavior": "comportamiento observado", "evidence": "evidencia", "acceptance_criteria": "criterios de aceptación", "risk": "riesgo", "security_impact": "impacto de seguridad", "impact": "impacto", "timeline": "cronología", "detection": "detección", "mitigation": "mitigación", "non_goals": "fuera de alcance"},
	}
	if label := labels[language][section]; label != "" {
		return label
	}
	return strings.Title(strings.ReplaceAll(section, "_", " "))
}

func noConcreteRiskClaim(language string) string {
	switch language {
	case "zh-CN":
		return "仅凭提供的 Issue 上下文，无法作出具体风险判断。"
	case "ja":
		return "提供された Issue コンテキストだけでは、具体的なリスク判断はできません。"
	case "es":
		return "No puede hacerse una afirmación concreta de riesgo solo con el contexto de la incidencia proporcionado."
	default:
		return "No concrete risk claim can be made from the supplied Issue context alone."
	}
}
