export default function({ user }: { user: string }) {
  return (
    <TestProfileKappa name={user}>
      <TestAvatarLambda size="large" />
    </TestProfileKappa>
  );
}
