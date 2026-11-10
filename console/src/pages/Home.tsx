import { IdentityCard, YourAccessPoints, YourUsage } from '../components/YourAccess'

/** Home for callers without an operator role: who they are, what admits them, and what they have used. */
export default function Home() {
  return (
    <div className="space-y-8">
      <header>
        <h1 className="text-2xl font-bold">Home</h1>
        <p className="text-gray-600 dark:text-gray-400">Who you are, the access points that admit you, and your own usage.</p>
      </header>
      <IdentityCard />
      <YourAccessPoints />
      <YourUsage />
    </div>
  )
}
