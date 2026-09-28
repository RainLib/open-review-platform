import { renderSetupPage, type SetupSearchParams } from "./_render";

export default async function SetupPage({
  searchParams,
}: {
  searchParams: Promise<SetupSearchParams>;
}) {
  return renderSetupPage(await searchParams);
}
