import type { Control, FieldValues, Path, UseFormSetError } from "react-hook-form";
import { useController } from "react-hook-form";
import { fieldErrors, problemMessage } from "@/shared/api/client";
import { TextField, type TextFieldProps } from "./index";
import { toast } from "./toast";

type FormTextFieldProps<T extends FieldValues> = Omit<
  TextFieldProps,
  "name" | "value" | "onChange" | "errorMessage"
> & {
  control: Control<T>;
  name: Path<T>;
};

/** Текстовое поле, связанное с react-hook-form. */
export function FormTextField<T extends FieldValues>({ control, name, ...props }: FormTextFieldProps<T>) {
  const { field, fieldState } = useController({ control, name });
  return (
    <TextField
      {...props}
      name={field.name}
      value={(field.value as string | undefined) ?? ""}
      onChange={field.onChange}
      onBlur={field.onBlur}
      errorMessage={fieldState.error?.message}
      validationBehavior="aria"
    />
  );
}

/**
 * Переносит ошибки полей из ответа API в форму; остальное показывает уведомлением.
 * rename сопоставляет имена полей API и формы, если они различаются.
 */
export function applyServerErrors<T extends FieldValues>(
  setError: UseFormSetError<T>,
  err: unknown,
  rename: Record<string, Path<T>> = {},
) {
  const fields = Object.entries(fieldErrors(err));
  if (fields.length === 0) {
    toast.error(problemMessage(err));
    return;
  }
  for (const [field, message] of fields) {
    setError(rename[field] ?? (field as Path<T>), { type: "server", message });
  }
}
