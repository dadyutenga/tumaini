import { useState } from "react";
import { MessageCircle, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, FormDialog, NativeSelect } from "@/components/shared/kit";
import { whatsappUrl } from "@/features/services/helpers";
import { useWrite } from "@/hooks/useApi";
import { ApiError } from "@/lib/api";

export type OutboundChannel = "sms" | "whatsapp" | "email";

/**
 * Reusable trigger + dialog for queueing a customer message via the backend outbox.
 * Only reveals the manual WhatsApp click-to-chat link after a 503 channel_unavailable error.
 */
export function SendOutboundButton({
  label,
  title,
  path,
  extraBody,
  successMessage,
  phone,
  whatsappText,
  idempotent = true,
  size = "default",
  variant = "outline",
  disabled,
}: {
  label: string;
  title: string;
  path: string;
  extraBody?: Record<string, string | undefined>;
  successMessage: string;
  phone?: string | null;
  whatsappText: string;
  idempotent?: boolean;
  size?: "default" | "sm" | "lg" | "icon";
  variant?: "default" | "outline" | "ghost" | "secondary";
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [channel, setChannel] = useState<OutboundChannel>("sms");
  const [unavailable, setUnavailable] = useState(false);
  const [noRecipient, setNoRecipient] = useState(false);

  const send = useWrite<Record<string, unknown>>("POST", path, {
    idempotent,
    success: successMessage,
    onSuccess: () => {
      setUnavailable(false);
      setNoRecipient(false);
      setOpen(false);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 503) {
        setUnavailable(true);
        setNoRecipient(false);
      } else if (err instanceof ApiError && err.code === "no_recipient") {
        setNoRecipient(true);
        setUnavailable(false);
      }
    },
  });

  return (
    <>
      <Button
        type="button"
        size={size}
        variant={variant}
        disabled={disabled}
        onClick={() => {
          setUnavailable(false);
          setNoRecipient(false);
          setOpen(true);
        }}
      >
        <Send size={14} /> {label}
      </Button>
      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title={title}
        description="Choose how to deliver this message through the business outbox."
        submitLabel="Send"
        busy={send.isPending}
        onSubmit={() => {
          setUnavailable(false);
          setNoRecipient(false);
          send.mutate({ channel, ...(extraBody ?? {}) });
        }}
      >
        <Field label="Channel">
          <NativeSelect
            value={channel}
            onChange={(e) => {
              setChannel(e.target.value as OutboundChannel);
              setUnavailable(false);
              setNoRecipient(false);
            }}
          >
            <option value="sms">SMS</option>
            <option value="whatsapp">WhatsApp</option>
            <option value="email">Email</option>
          </NativeSelect>
        </Field>
        {noRecipient && (
          <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            This customer has no phone number or email for the selected channel.
          </p>
        )}
        {unavailable && (
          <div className="space-y-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-xs">
            <p className="text-amber-800 dark:text-amber-300">
              Automatic delivery is not configured for this channel. You can still send it manually
              in WhatsApp:
            </p>
            <Button type="button" size="sm" variant="outline" asChild>
              <a
                href={whatsappUrl(phone || undefined, whatsappText)}
                target="_blank"
                rel="noreferrer"
              >
                <MessageCircle size={14} /> Open in WhatsApp
              </a>
            </Button>
          </div>
        )}
      </FormDialog>
    </>
  );
}
