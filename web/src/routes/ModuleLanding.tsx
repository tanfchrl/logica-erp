import { Link } from '@tanstack/react-router';
import { ArrowRight } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { modules } from '@/lib/doctypes';

export function ModuleLanding({ modulePath }: { modulePath: string }) {
  const mod = modules.find((m) => m.path === modulePath);
  if (!mod) return null;

  return (
    <>
      <PageHeader
        crumbs={[{ label: mod.name }]}
        title={mod.name}
        subtitle={mod.description}
      />
      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {mod.doctypes.map((dt) => {
            const Icon = dt.icon;
            return (
              <Link
                key={dt.slug + dt.modulePath}
                to={`${dt.modulePath}/${dt.slug}` as never}
                className="block focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 rounded-lg"
              >
                <Card className="group hover:border-border-strong hover:shadow-overlay transition-all cursor-pointer">
                  <div className="flex items-start justify-between mb-3">
                    <div className="inline-flex size-10 items-center justify-center rounded-lg bg-accent-soft text-accent group-hover:bg-accent group-hover:text-accent-fg transition-colors">
                      <Icon className="size-5" />
                    </div>
                    <ArrowRight className="size-4 text-text-tertiary group-hover:text-accent group-hover:translate-x-0.5 transition-all" />
                  </div>
                  <CardTitle>{dt.title}</CardTitle>
                  <CardDescription>Open the {dt.singular.toLowerCase()} list.</CardDescription>
                </Card>
              </Link>
            );
          })}
        </div>
      </div>
    </>
  );
}
