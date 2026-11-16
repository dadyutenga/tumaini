import { useState } from "react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Field, PageHeader } from "@/components/shared/kit";
import { useAuth } from "@/features/auth/auth";
import { useWrite } from "@/hooks/useApi";

export default function Profile() {
  const { user, me } = useAuth();
  const [name, setName] = useState(user?.name ?? "");
  const [pw, setPw] = useState({ current_password: "", new_password: "" });
  const update = useWrite<{ name: string }, unknown>("PUT", "/me", { success: "Profile updated" });
  const change = useWrite<typeof pw, unknown>("PUT", "/auth/password", {
    success: "Password changed",
    onSuccess: () => setPw({ current_password: "", new_password: "" }),
  });
  return (
    <>
      <PageHeader
        title="My account"
        description={`${user?.role_name}${me?.is_owner ? " · owner" : ""}`}
      />
      <div className="grid max-w-3xl gap-6 md:grid-cols-2">
        <form
          className="space-y-4 rounded-2xl border bg-card p-6"
          onSubmit={(e) => {
            e.preventDefault();
            update.mutate({ name });
          }}
        >
          <h2 className="font-semibold">Profile</h2>
          <Field label="Name">
            <Input required value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Phone or email">
            <Input disabled value={user?.phone ?? user?.email ?? ""} />
          </Field>
          <Button type="submit" disabled={update.isPending}>
            Save
          </Button>
        </form>
        <form
          className="space-y-4 rounded-2xl border bg-card p-6"
          onSubmit={(e) => {
            e.preventDefault();
            change.mutate(pw);
          }}
        >
          <h2 className="font-semibold">Change password</h2>
          <Field label="Current password">
            <Input
              required
              type="password"
              autoComplete="current-password"
              value={pw.current_password}
              onChange={(e) => setPw({ ...pw, current_password: e.target.value })}
            />
          </Field>
          <Field label="New password">
            <Input
              required
              minLength={8}
              type="password"
              autoComplete="new-password"
              value={pw.new_password}
              onChange={(e) => setPw({ ...pw, new_password: e.target.value })}
            />
          </Field>
          <Button type="submit" disabled={change.isPending}>
            Change password
          </Button>
        </form>
      </div>
    </>
  );
}
