import Link from 'next/link';
import { useRouter } from 'next/router';

const navigation = [
  {
    title: 'Getting Started',
    items: [
      { title: 'Introduction', href: '/' },
      { title: 'Installation', href: '/getting-started/installation' },
      { title: 'Quick Start', href: '/getting-started/quickstart' },
    ],
  },
  {
    title: 'CLI Commands',
    items: [
      { title: 'verify', href: '/cli/verify' },
      { title: 'chat', href: '/cli/chat' },
      { title: 'mission', href: '/cli/mission' },
      { title: 'vars', href: '/cli/vars' },
    ],
  },
  {
    title: 'Configuration',
    items: [
      { title: 'Overview', href: '/config/overview' },
      { title: 'Variables', href: '/config/variables' },
      { title: 'Models', href: '/config/models' },
      { title: 'Agents', href: '/config/agents' },
      { title: 'Tools', href: '/config/tools' },
      { title: 'Plugins', href: '/config/plugins' },
    ],
  },
  {
    title: 'Missions',
    items: [
      { title: 'Overview', href: '/missions/overview' },
      { title: 'Tasks', href: '/missions/tasks' },
      { title: 'Datasets', href: '/missions/datasets' },
      { title: 'Iteration', href: '/missions/iteration' },
      { title: 'Internal Tools', href: '/missions/internal-tools' },
    ],
  },
];

export default function Layout({ children }) {
  const router = useRouter();

  return (
    <div className="layout">
      <aside className="sidebar">
        <Link href="/" className="sidebar-logo">
          Squadron
        </Link>
        <nav>
          <ul>
            {navigation.map((section) => (
              <li key={section.title}>
                <span className="section-title">{section.title}</span>
                <ul>
                  {section.items.map((item) => (
                    <li key={item.href}>
                      <Link
                        href={item.href}
                        className={router.pathname === item.href ? 'active' : ''}
                      >
                        {item.title}
                      </Link>
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        </nav>
      </aside>
      <main className="main-content">
        <article className="content">{children}</article>
      </main>
    </div>
  );
}
