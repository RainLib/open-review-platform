import { Avatar, AvatarFallback, AvatarGroup, AvatarGroupCount } from "@/components/ui/avatar";

const avatarTones = ["bg-violet-100 text-violet-800", "bg-sky-100 text-sky-800", "bg-emerald-100 text-emerald-800", "bg-amber-100 text-amber-800"];

export function People({ people, max = 3 }: { people: string[]; max?: number }) {
  const visible = people.slice(0, max);
  const extra = people.length - visible.length;

  return (
    <AvatarGroup aria-label={`参与者：${people.join("、")}`}>
      {visible.map((person, index) => (
        <Avatar key={person} size="sm">
          <AvatarFallback className={avatarTones[index % avatarTones.length]}>{person}</AvatarFallback>
        </Avatar>
      ))}
      {extra > 0 ? <AvatarGroupCount className="size-6 text-xs">+{extra}</AvatarGroupCount> : null}
    </AvatarGroup>
  );
}
