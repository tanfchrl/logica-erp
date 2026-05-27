import { useRef } from 'react';
import { Image as ImageIcon, RotateCcw, Sun, Moon, Upload, Trash2 } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { cn } from '@/lib/cn';
import { useUI } from '@/store/ui';

export function AppearanceSection() {
  const { theme, setTheme, brand, setBrand, resetBrand } = useUI();
  const fileInput = useRef<HTMLInputElement>(null);

  async function handleLogoPicked(file: File) {
    if (!file.type.startsWith('image/')) return;
    if (file.size > 512 * 1024) {
      alert('Please pick an image under 512 KB.');
      return;
    }
    const dataUrl = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload  = () => resolve(reader.result as string);
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });
    setBrand({ logoDataUrl: dataUrl });
  }

  return (
    <div className="space-y-6">

      {/* ---- Theme ---- */}
      <section>
        <div className="mb-3">
          <CardTitle>Theme</CardTitle>
          <CardDescription>Switch between light and dark surfaces.</CardDescription>
        </div>
        <Card>
          <div className="inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
            <ThemeChip active={theme === 'light'} icon={Sun}  label="Light" onClick={() => setTheme('light')} />
            <ThemeChip active={theme === 'dark'}  icon={Moon} label="Dark"  onClick={() => setTheme('dark')} />
          </div>
        </Card>
      </section>

      {/* ---- Brand ---- */}
      <section>
        <div className="mb-3 flex items-end justify-between gap-3 flex-wrap">
          <div>
            <CardTitle>Brand</CardTitle>
            <CardDescription>
              How your workspace appears in the sidebar and on the login screen.
            </CardDescription>
          </div>
          <Button variant="ghost" size="sm" onClick={() => { if (confirm('Reset brand to defaults?')) resetBrand(); }}>
            <RotateCcw className="size-3.5" /> Reset
          </Button>
        </div>

        <Card>
          <div className="grid sm:grid-cols-[88px_1fr] gap-5 items-start">
            <div className="flex flex-col items-center gap-2">
              <BrandPreview brand={brand} />
              <div className="text-caption text-stone">Preview</div>
            </div>

            <div className="space-y-4">
              <Field label="Workspace name">
                <Input
                  value={brand.name}
                  maxLength={60}
                  onChange={(e) => setBrand({ name: e.target.value })}
                />
              </Field>

              <Field label="Tagline" hint="Shown in muted text below the workspace name.">
                <Input
                  value={brand.tagline}
                  maxLength={80}
                  onChange={(e) => setBrand({ tagline: e.target.value })}
                />
              </Field>

              <Field label="Brand mark" hint="One or two characters. Shown when no logo is uploaded.">
                <Input
                  value={brand.mark}
                  maxLength={2}
                  className="w-24 text-center font-semibold uppercase"
                  onChange={(e) => setBrand({ mark: e.target.value.toUpperCase() })}
                />
              </Field>

              <div className="space-y-1.5">
                <div className="label-base">Logo</div>
                <div className="flex items-center gap-2">
                  <Button variant="secondary" size="sm" onClick={() => fileInput.current?.click()}>
                    <Upload className="size-3.5" />
                    {brand.logoDataUrl ? 'Replace logo' : 'Upload logo'}
                  </Button>
                  {brand.logoDataUrl && (
                    <Button variant="ghost" size="sm" onClick={() => setBrand({ logoDataUrl: null })}>
                      <Trash2 className="size-3.5" /> Remove
                    </Button>
                  )}
                  <input
                    ref={fileInput}
                    type="file"
                    accept="image/png,image/svg+xml,image/jpeg,image/webp"
                    className="hidden"
                    onChange={(e) => {
                      const f = e.target.files?.[0];
                      if (f) void handleLogoPicked(f);
                      e.target.value = '';
                    }}
                  />
                </div>
                <p className="text-caption text-stone">PNG, SVG, JPG or WebP. Square works best. Max 512 KB.</p>
              </div>
            </div>
          </div>
        </Card>
      </section>

    </div>
  );
}

function ThemeChip({
  active, icon: Icon, label, onClick,
}: { active: boolean; icon: React.ComponentType<{ className?: string }>; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 h-8 px-3 rounded-full text-body-sm transition-colors',
        active ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
      )}
    >
      <Icon className="size-3.5" />
      {label}
    </button>
  );
}

function BrandPreview({ brand }: { brand: { mark: string; logoDataUrl: string | null } }) {
  return (
    <div className="size-16 rounded-xl bg-primary text-primary-fg flex items-center justify-center overflow-hidden">
      {brand.logoDataUrl
        ? <img src={brand.logoDataUrl} alt="" className="size-full object-contain" />
        : <span className="text-xl font-semibold">{brand.mark || <ImageIcon className="size-5 opacity-50" />}</span>}
    </div>
  );
}
