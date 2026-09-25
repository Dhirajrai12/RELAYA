import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

export interface Option {
  value: string
  label: string
}

/** A single-value select over plain string options. */
export function SimpleSelect({
  value,
  onChange,
  options,
  placeholder,
  className,
  id,
}: {
  value: string
  onChange: (value: string) => void
  options: Option[]
  placeholder?: string
  className?: string
  id?: string
}) {
  return (
    <Select items={options} value={value} onValueChange={(v) => onChange((v as string | null) ?? '')}>
      <SelectTrigger id={id} className={className}>
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
