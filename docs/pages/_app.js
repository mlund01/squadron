import { useEffect } from 'react';
import { useRouter } from 'next/router';
import '../styles/globals.css';
import Layout from '../components/Layout';

export default function App({ Component, pageProps }) {
  const router = useRouter();

  useEffect(() => {
    // Highlight code blocks after page loads
    const highlight = () => {
      if (typeof window !== 'undefined' && window.Prism) {
        window.Prism.highlightAll();
      }
    };

    // Initial highlight
    highlight();

    // Re-highlight on route changes
    router.events.on('routeChangeComplete', highlight);
    return () => {
      router.events.off('routeChangeComplete', highlight);
    };
  }, [router.events]);

  return (
    <Layout>
      <Component {...pageProps} />
    </Layout>
  );
}
